package tunnel

import (
	"fmt"
	"io"
	"log/slog"
	"net"
)

// Proxy forwards a TCP connection from bindIP:localPort to vmIP:22.
// bindIP must be the provider's WireGuard address so the relay port is only
// reachable by the control plane over the tunnel — never on the provider's
// public interface. If bindIP is empty it falls back to loopback (same-host
// testing) rather than 0.0.0.0.
func Proxy(bindIP string, localPort int, vmIP string) error {
	if bindIP == "" {
		bindIP = "127.0.0.1"
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", bindIP, localPort))
	if err != nil {
		return fmt.Errorf("listen %s:%d: %w", bindIP, localPort, err)
	}
	slog.Info("tunnel: listening", "bind", bindIP, "port", localPort, "target", vmIP+":22")
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
