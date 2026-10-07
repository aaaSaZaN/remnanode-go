package lock

import (
	"log"
	"net"
)

var lockListener net.Listener

// acquire instance lock
func AcquireInstanceLock() bool {
	l, err := net.Listen("unix", "\x00rwnode-lock")
	if err != nil {
		// try filesystem fallback on non-linux
		l, err = net.Listen("unix", "/tmp/rwnode-lock.sock")
		if err != nil {
			return false
		}
	}

	lockListener = l
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	return true
}

func ReleaseInstanceLock() {
	if lockListener != nil {
		_ = lockListener.Close()
	}
}

func DuplicateInstanceMessage() string {
	return "[WARN] Another instance of Remnawave Node is already running!"
}

func PrintStartMessage(port int, xrayVersion string, cpus int, memTotal uint64) {
	log.Printf("[START] Remnawave Node started on port :%d (Xray: %s, %d CPUs)", port, xrayVersion, cpus)
}
