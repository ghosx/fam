package vault

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tmpPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "fam.db")
}

// V01 创建后可解密出原始内容
func TestV01_CreateAndOpen(t *testing.T) {
	path := tmpPath(t)
	body := []byte("SQLite format 3\x00 明文数据库镜像内容")
	v, err := Create(path, "pass123456", body)
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if v.Path() != path {
		t.Fatalf("路径不正确: %s", v.Path())
	}
	_, got, err := Open(path, "pass123456")
	if err != nil {
		t.Fatalf("打开失败: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("解密内容不一致")
	}
}

// V02 磁盘上不出现明文
func TestV02_CiphertextOnDisk(t *testing.T) {
	path := tmpPath(t)
	secret := "招商银行储蓄卡-158000.00"
	if _, err := Create(path, "pass123456", []byte(secret)); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(secret)) {
		t.Fatalf("文件中不应出现明文")
	}
	if bytes.Contains(raw, []byte("SQLite format 3")) {
		t.Fatalf("文件中不应出现 SQLite 文件头")
	}
	if string(raw[:8]) != magic {
		t.Fatalf("文件头不正确")
	}
}

// V03 错误密码
func TestV03_WrongPassword(t *testing.T) {
	path := tmpPath(t)
	if _, err := Create(path, "pass123456", []byte("data")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Open(path, "wrong-pass"); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("应返回 ErrWrongPassword，实际 %v", err)
	}
}

// V04 保存后内容更新且可再次解密
func TestV04_Save(t *testing.T) {
	path := tmpPath(t)
	v, err := Create(path, "pass123456", []byte("v1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Save([]byte("v2-longer-content")); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	_, body, err := Open(path, "pass123456")
	if err != nil {
		t.Fatalf("重新打开失败: %v", err)
	}
	if string(body) != "v2-longer-content" {
		t.Fatalf("保存后内容不正确: %q", body)
	}
}

// V05 每次保存使用不同随机数（密文不同）
func TestV05_NonceRandomized(t *testing.T) {
	path := tmpPath(t)
	v, _ := Create(path, "pass123456", []byte("same"))
	first, _ := os.ReadFile(path)
	if err := v.Save([]byte("same")); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if bytes.Equal(first, second) {
		t.Fatalf("两次保存的密文不应相同")
	}
}

// V06 改密码：旧密码失效、新密码可用、数据不变
func TestV06_ChangePassword(t *testing.T) {
	path := tmpPath(t)
	v, err := Create(path, "old-pass-123", []byte("payload"))
	if err != nil {
		t.Fatal(err)
	}
	if err := v.ChangePassword("new-pass-456", []byte("payload")); err != nil {
		t.Fatalf("改密码失败: %v", err)
	}
	if _, _, err := Open(path, "old-pass-123"); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("旧密码应失效，实际 %v", err)
	}
	_, body, err := Open(path, "new-pass-456")
	if err != nil {
		t.Fatalf("新密码应可用: %v", err)
	}
	if string(body) != "payload" {
		t.Fatalf("数据不一致: %q", body)
	}
}

// V07 篡改正文被检测
func TestV07_TamperBody(t *testing.T) {
	path := tmpPath(t)
	if _, err := Create(path, "pass123456", []byte("payload-data")); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	raw[len(raw)-1] ^= 0xFF
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Open(path, "pass123456"); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("篡改应被检测，实际 %v", err)
	}
}

// V08 篡改头部（salt）被检测
func TestV08_TamperHeader(t *testing.T) {
	path := tmpPath(t)
	if _, err := Create(path, "pass123456", []byte("payload")); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	raw[20] ^= 0x01 // 改动 salt
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Open(path, "pass123456"); err == nil {
		t.Fatalf("头部被篡改应失败")
	}
}

// V09 不存在的文件
func TestV09_NotInitialized(t *testing.T) {
	if _, _, err := Open(filepath.Join(t.TempDir(), "none.db"), "x"); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("应返回 ErrNotInitialized，实际 %v", err)
	}
	if Exists(filepath.Join(t.TempDir(), "none.db")) {
		t.Fatalf("不存在时应返回 false")
	}
}

// V13 Exists 只认加密容器（普通文件不算）
func TestV13_ExistsRequiresMagic(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain.db")
	if err := os.WriteFile(plain, []byte("SQLite format 3\x00 filler"), 0o600); err != nil {
		t.Fatal(err)
	}
	if Exists(plain) {
		t.Fatalf("普通 SQLite 文件不应被视为加密容器")
	}
	path := filepath.Join(dir, "vault.db")
	if _, err := Create(path, "pass123456", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if !Exists(path) {
		t.Fatalf("加密容器应被识别")
	}
}

// V10 非容器文件
func TestV10_NotAVault(t *testing.T) {
	path := tmpPath(t)
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 4096)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Open(path, "pass123456"); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("非容器文件应返回 ErrCorrupt，实际 %v", err)
	}
}

// V11 Verify 口令校验
func TestV11_Verify(t *testing.T) {
	path := tmpPath(t)
	v, _ := Create(path, "pass123456", []byte("x"))
	if !v.Verify("pass123456") {
		t.Fatalf("正确口令应通过")
	}
	if v.Verify("bad") {
		t.Fatalf("错误口令不应通过")
	}
}

// V12 保存时文件被完整替换（无 .tmp 残留）
func TestV12_NoTempLeftover(t *testing.T) {
	path := tmpPath(t)
	v, _ := Create(path, "pass123456", []byte("x"))
	if err := v.Save([]byte("y")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("不应残留临时文件")
	}
}
