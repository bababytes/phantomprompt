package proxy

import (
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"
)

func TestConnectionPreservesExpiredDeadlines(t *testing.T) {
	for _, operation := range []string{"read", "write"} {
		t.Run(operation, func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()
			conn := &limitedConn{Conn: client}
			if err := conn.SetDeadline(time.Now().Add(-time.Second)); err != nil {
				t.Fatal(err)
			}
			completed := make(chan error, 1)
			go func() {
				var err error
				if operation == "read" {
					_, err = conn.Read(make([]byte, 1))
				} else {
					_, err = conn.Write([]byte("x"))
				}
				completed <- err
			}()
			select {
			case err := <-completed:
				if !errors.Is(err, os.ErrDeadlineExceeded) {
					t.Fatalf("expected deadline error, got %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("I/O replaced an expired caller deadline")
			}
			if err := conn.SetDeadline(time.Time{}); err != nil {
				t.Fatal(err)
			}
			peerDone := make(chan error, 1)
			if operation == "read" {
				go func() { _, err := server.Write([]byte("x")); peerDone <- err }()
				_, err := conn.Read(make([]byte, 1))
				if err != nil {
					t.Fatal(err)
				}
			} else {
				go func() { _, err := io.ReadFull(server, make([]byte, 1)); peerDone <- err }()
				_, err := conn.Write([]byte("x"))
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := <-peerDone; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestConnectionDeadlineBounds(t *testing.T) {
	conn := &limitedConn{}
	earlier := time.Now().Add(time.Second)
	if got := conn.deadline(earlier); !got.Equal(earlier) {
		t.Fatal("caller deadline was extended")
	}
	if got := conn.deadline(time.Time{}); got.After(time.Now().Add(idleTimeout)) {
		t.Fatal("missing idle cap")
	}
}

// A keep-alive tunnel used to be cut 10 minutes after it was accepted, even in the middle of a stream.
func TestActiveConnectionHasNoLifetimeCap(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tracked := Track(listener, 1)
	defer tracked.Close()
	go func() {
		if peer, err := net.Dial("tcp", listener.Addr().String()); err == nil {
			peer.Write([]byte("x"))
			time.Sleep(time.Second)
			peer.Close()
		}
	}()
	conn, err := tracked.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if got := conn.(*limitedConn).deadline(time.Time{}); time.Until(got) < idleTimeout-time.Second {
		t.Fatalf("deadline in %v is shorter than the idle timeout", time.Until(got))
	}
	if _, err := conn.Read(make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
}
