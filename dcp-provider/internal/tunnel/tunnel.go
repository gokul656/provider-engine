package tunnel

import (
	"fmt"
	"io"
	"log/slog"
	"net"
)

// Proxy forwards a TCP connection from localPort to vmIP:22.
// The control plane connects to localPort; the proxy forwards to the VM.
func Proxy(localPort int, vmIP string) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", localPort))
	if err != nil {
		return fmt.Errorf("listen :%d: %w", localPort, err)
	}
	slog.Info("tunnel: listening", "port", localPort, "target", vmIP+":22")
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go forward(conn, vmIP+":22")
	}
}

func forward(src net.Conn, dst string) {
	defer src.Close()
	target, err := net.Dial("tcp", dst)
	if err != nil {
		slog.Warn("tunnel: dial target failed", "dst", dst, "err", err)
		return
	}
	defer target.Close()
	done := make(chan struct{}, 2)
	go func() { io.Copy(target, src); done <- struct{}{} }()
	go func() { io.Copy(src, target); done <- struct{}{} }()
	<-done
}
