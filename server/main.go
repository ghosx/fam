// Command fam-server 启动 fam 的本机后端服务。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"fam/server/internal/api"
	"fam/server/internal/store"
)

func main() {
	dbPath := flag.String("db", "", "数据库文件路径（默认 %LOCALAPPDATA%/fam/fam.db，可用 FAM_DB 覆盖）")
	webDir := flag.String("web", "", "前端静态资源目录（默认自动探测 app/src）")
	addr := flag.String("addr", "127.0.0.1:0", "监听地址，端口为 0 时由系统分配")
	flag.Parse()

	path := resolveDBPath(*dbPath)
	st, err := store.Open(path)
	if err != nil {
		log.Fatalf("启动失败：%v", err)
	}
	defer st.Close()

	dir := resolveWebDir(*webDir)
	handler := api.New(st, dir)

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("监听失败：%v", err)
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}

	// 父进程（Electron）通过这行输出获取实际端口
	tcpAddr, _ := ln.Addr().(*net.TCPAddr)
	fmt.Printf("FAM_READY %d\n", tcpAddr.Port)
	log.Printf("fam 后端已启动 port=%d db=%s", tcpAddr.Port, path)

	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("服务异常：%v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("关闭服务：%v", err)
	}
	log.Printf("fam 后端已退出")
}

// resolveDBPath 确定数据库文件路径。
func resolveDBPath(flagPath string) string {
	if flagPath != "" {
		return flagPath
	}
	if v := os.Getenv("FAM_DB"); v != "" {
		return v
	}
	return filepath.Join(defaultHome(), "fam.db")
}

// resolveWebDir 确定前端静态资源目录。
func resolveWebDir(flagDir string) string {
	cands := []string{}
	if flagDir != "" {
		cands = append(cands, flagDir)
	}
	if exe, err := os.Executable(); err == nil {
		cands = append(cands,
			filepath.Join(filepath.Dir(exe), "..", "app", "src"),
			filepath.Join(filepath.Dir(exe), "app", "src"),
		)
	}
	cwd, _ := os.Getwd()
	if cwd != "" {
		cands = append(cands,
			filepath.Join(cwd, "..", "app", "src"),
			filepath.Join(cwd, "app", "src"),
			filepath.Join(cwd, "..", "..", "app", "src"),
		)
	}
	for _, c := range cands {
		if abs, err := filepath.Abs(c); err == nil {
			c = abs
		}
		if st, err := os.Stat(filepath.Join(c, "index.html")); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

// defaultHome 返回数据目录。
func defaultHome() string {
	if v := os.Getenv("FAM_HOME"); v != "" {
		return v
	}
	if v := os.Getenv("LOCALAPPDATA"); v != "" {
		return filepath.Join(v, "fam")
	}
	if v := os.Getenv("HOME"); v != "" {
		return filepath.Join(v, ".fam")
	}
	return "."
}
