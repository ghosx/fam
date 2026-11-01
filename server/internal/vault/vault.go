// Package vault 实现 fam 的加密存储容器：
// 整个数据库以 AES-256-GCM 密文落盘，运行时只在内存中解密，磁盘上不出现明文。
//
// 文件布局（多字节整数均为大端）：
//
//	0   magic "FAMVAULT"(8)
//	8   version(1) = 1
//	9   kdf(1)     = 1  (PBKDF2-HMAC-SHA256)
//	10  reserved(2)
//	12  iterations(4)
//	16  salt(16)
//	32  kekNonce(12)
//	44  wrappedDEK(48)  = 32 字节 DEK + 16 字节 GCM tag
//	92  bodyNonce(12)
//	104 body ciphertext(变长)  = SQLite 镜像 + 16 字节 GCM tag
//
// KEK 由口令经 PBKDF2 派生，只用于包裹随机的 DEK；正文用 DEK 加密，
// 因此修改口令只需用新 KEK 重新包裹 DEK，不必重写整库（虽然实现上会一并重写）。
// 头部作为 AAD 参与校验，任何字节改动都会导致解密失败。
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// 常量定义。
const (
	magic       = "FAMVAULT"
	version     = 1
	kdfPBKDF2   = 1
	Iterations  = 120000
	saltLen     = 16
	nonceLen    = 12
	dekLen      = 32
	keyLen      = 32
	headerFixed = 104
	dekBlock    = 48
	tagLen      = 16
)

// 错误定义。
var (
	ErrNotInitialized = errors.New("尚未设置访问密码")
	ErrWrongPassword  = errors.New("密码不正确")
	ErrCorrupt        = errors.New("数据文件已损坏或被篡改")
)

// Vault 表示一个已解锁的加密容器（内存中持有 KEK 与 DEK）。
type Vault struct {
	path  string
	salt  []byte
	iters int
	kek   []byte
	dek   []byte
}

// Path 返回容器文件路径。
func (v *Vault) Path() string { return v.path }

// Exists 判断该路径是否存在一个合法的加密容器（校验文件头魔数）。
func Exists(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 8)
	n, _ := f.Read(buf)
	return n == 8 && string(buf) == magic
}

// Create 用口令创建新的加密容器，body 为初始明文数据库镜像。
func Create(path, password string, body []byte) (*Vault, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("生成盐失败: %w", err)
	}
	kek, err := derive(password, salt, Iterations)
	if err != nil {
		return nil, err
	}
	dek := make([]byte, dekLen)
	if _, err := rand.Read(dek); err != nil {
		return nil, fmt.Errorf("生成数据密钥失败: %w", err)
	}
	v := &Vault{path: path, salt: salt, iters: Iterations, kek: kek, dek: dek}
	if err := v.write(body); err != nil {
		return nil, err
	}
	return v, nil
}

// Open 用口令打开容器，返回容器对象与解密后的明文数据库镜像。
func Open(path, password string) (*Vault, []byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, ErrNotInitialized
		}
		return nil, nil, err
	}
	if len(raw) < headerFixed+tagLen || string(raw[:8]) != magic {
		return nil, nil, ErrCorrupt
	}
	if raw[8] != version || raw[9] != kdfPBKDF2 {
		return nil, nil, fmt.Errorf("%w: 不支持的文件版本", ErrCorrupt)
	}
	iters := int(readUint32(raw[12:16]))
	if iters < 10000 || iters > 5000000 {
		return nil, nil, ErrCorrupt
	}
	salt := append([]byte(nil), raw[16:32]...)
	kekNonce := raw[32:44]
	wrapped := raw[44:92]
	bodyNonce := raw[92:104]

	kek, err := derive(password, salt, iters)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := newGCM(kek)
	if err != nil {
		return nil, nil, err
	}
	dek, err := gcm.Open(nil, kekNonce, wrapped, aadDEK(raw[:32]))
	if err != nil {
		return nil, nil, ErrWrongPassword
	}
	bodyGCM, err := newGCM(dek)
	if err != nil {
		return nil, nil, err
	}
	body, err := bodyGCM.Open(nil, bodyNonce, raw[headerFixed:], aadBody(raw[:104]))
	if err != nil {
		return nil, nil, ErrCorrupt
	}
	return &Vault{path: path, salt: salt, iters: iters, kek: kek, dek: dek}, body, nil
}

// Verify 校验口令是否与当前容器一致（常量时间比较）。
func (v *Vault) Verify(password string) bool {
	if v == nil || len(v.kek) != keyLen {
		return false
	}
	kek, err := derive(password, v.salt, v.iters)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(kek, v.kek) == 1
}

// KEK 返回当前密钥加密密钥（供备份校验使用，仅在内存中）。
func (v *Vault) KEK() []byte { return v.kek }

// DEK 返回当前数据密钥（供导入备份使用，仅在内存中）。
func (v *Vault) DEK() []byte { return v.dek }

// OpenWithKEK 用已知 KEK 打开容器（备份与当前容器同盐时可用）。
func OpenWithKEK(path string, kek []byte) (*Vault, []byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	if len(raw) < headerFixed+tagLen || string(raw[:8]) != magic {
		return nil, nil, ErrCorrupt
	}
	if iters := int(readUint32(raw[12:16])); iters < 10000 || iters > 5000000 {
		return nil, nil, ErrCorrupt
	}
	gcm, err := newGCM(kek)
	if err != nil {
		return nil, nil, err
	}
	dek, err := gcm.Open(nil, raw[32:44], raw[44:92], aadDEK(raw[:32]))
	if err != nil {
		return nil, nil, ErrWrongPassword
	}
	bodyGCM, err := newGCM(dek)
	if err != nil {
		return nil, nil, err
	}
	body, err := bodyGCM.Open(nil, raw[92:104], raw[headerFixed:], aadBody(raw[:104]))
	if err != nil {
		return nil, nil, ErrCorrupt
	}
	v := &Vault{
		path:  path,
		salt:  append([]byte(nil), raw[16:32]...),
		iters: int(readUint32(raw[12:16])),
		kek:   kek,
		dek:   dek,
	}
	return v, body, nil
}

// Import 采用外来数据密钥并写入正文，保留当前 KEK（因此当前口令继续有效）。
func (v *Vault) Import(dek, body []byte) error {
	if len(dek) != dekLen {
		return errors.New("数据密钥长度异常")
	}
	v.dek = dek
	return v.write(body)
}

// Save 用当前密钥重新加密并原子写入。
func (v *Vault) Save(body []byte) error {
	if v == nil || len(v.dek) != dekLen {
		return errors.New("容器未解锁")
	}
	return v.write(body)
}

// ChangePassword 用新口令重新包裹同一个 DEK 并写入正文。
func (v *Vault) ChangePassword(newPassword string, body []byte) error {
	if v == nil || len(v.dek) != dekLen {
		return errors.New("容器未解锁")
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	kek, err := derive(newPassword, salt, Iterations)
	if err != nil {
		return err
	}
	v.salt = salt
	v.iters = Iterations
	v.kek = kek
	return v.write(body)
}

// write 组装头部 + 密文正文并原子落盘。
func (v *Vault) write(body []byte) error {
	gcm, err := newGCM(v.kek)
	if err != nil {
		return err
	}
	header := make([]byte, headerFixed)
	copy(header[0:8], magic)
	header[8] = version
	header[9] = kdfPBKDF2
	putUint32(header[12:16], uint32(v.iters))
	copy(header[16:32], v.salt)

	kekNonce := make([]byte, nonceLen)
	if _, err := rand.Read(kekNonce); err != nil {
		return err
	}
	copy(header[32:44], kekNonce)
	wrapped := gcm.Seal(nil, kekNonce, v.dek, aadDEK(header[:32]))
	if len(wrapped) != dekBlock {
		return errors.New("密钥包裹长度异常")
	}
	copy(header[44:92], wrapped)

	bodyGCM, err := newGCM(v.dek)
	if err != nil {
		return err
	}
	bodyNonce := make([]byte, nonceLen)
	if _, err := rand.Read(bodyNonce); err != nil {
		return err
	}
	copy(header[92:104], bodyNonce)
	ct := bodyGCM.Seal(nil, bodyNonce, body, aadBody(header[:104]))

	out := make([]byte, 0, headerFixed+len(ct))
	out = append(out, header...)
	out = append(out, ct...)
	return atomicWrite(v.path, out)
}

// atomicWrite 写入临时文件后原子替换，避免产生半截文件。
func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建数据目录失败: %w", err)
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// derive 由口令与盐派生 KEK。
func derive(password string, salt []byte, iters int) ([]byte, error) {
	key, err := pbkdf2.Key(sha256.New, password, salt, iters, keyLen)
	if err != nil {
		return nil, fmt.Errorf("派生密钥失败: %w", err)
	}
	return key, nil
}

// newGCM 构造 AES-256-GCM。
func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// aadDEK 包裹 DEK 时使用的附加数据。
func aadDEK(headerPrefix []byte) []byte {
	out := make([]byte, 0, len(headerPrefix)+4)
	out = append(out, headerPrefix...)
	return append(out, '|', 'd', 'e', 'k')
}

// aadBody 加密正文时使用的附加数据。
func aadBody(header []byte) []byte {
	out := make([]byte, 0, len(header)+5)
	out = append(out, header...)
	return append(out, '|', 'b', 'o', 'd', 'y')
}

func readUint32(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func putUint32(b []byte, v uint32) {
	b[0] = byte(v >> 24)
	b[1] = byte(v >> 16)
	b[2] = byte(v >> 8)
	b[3] = byte(v)
}
