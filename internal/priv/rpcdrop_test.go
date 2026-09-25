package priv

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestMain lets the test binary play an interface process: with RPCDROP_DAEMON set it listens on
// a port of its own (printed), calls the listener named in the variable, and waits for an answer
// that never comes - until its connection is shut from outside, when it prints "released".
func TestMain(m *testing.M) {
	if addr := os.Getenv("RPCDROP_DAEMON"); addr != "" {
		daemon(addr)
		return
	}
	os.Exit(m.Run())
}

func daemon(addr string) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Println("error", err)
		os.Exit(2)
	}
	c, err := net.Dial("tcp", addr)
	if err != nil {
		fmt.Println("error", err)
		os.Exit(2)
	}
	fmt.Printf("port %d\n", ln.Addr().(*net.TCPAddr).Port)
	_, _ = c.Write([]byte("POST / HTTP/1.1\r\nContent-Length: 0\r\n\r\n"))
	_ = c.SetReadDeadline(time.Now().Add(20 * time.Second))
	buf := make([]byte, 16)
	_, err = c.Read(buf)
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		fmt.Println("still hanging")
		os.Exit(1)
	}
	fmt.Println("released")
	os.Exit(0)
}

func interfacesList(t *testing.T, root string, ports ...int) {
	t.Helper()
	var b strings.Builder
	b.WriteString("<interfaces>\n")
	for _, p := range ports {
		fmt.Fprintf(&b, "<ipc><name>X%d</name><url>xmlrpc://127.0.0.1:%d</url></ipc>\n", p, p)
	}
	b.WriteString("<ipc><name>LAN</name><url>xmlrpc://192.0.2.227:2001</url></ipc>\n</interfaces>\n")
	if err := os.MkdirAll(filepath.Join(root, "etc/config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc/config/InterfacesList.xml"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The real thing: a process held in a read by a listener that never answers is released by
// dropRPC, found through the real /proc by the port it listens on, and nothing else is touched.
func TestDropRPCReleasesAHeldDaemon(t *testing.T) {
	hang, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer hang.Close()
	held := make(chan net.Conn, 1)
	go func() {
		c, err := hang.Accept()
		if err == nil {
			held <- c // read nothing back, answer nothing
		}
	}()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "RPCDROP_DAEMON="+hang.Addr().String())
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	lines := bufio.NewScanner(out)
	if !lines.Scan() || !strings.HasPrefix(lines.Text(), "port ") {
		t.Fatalf("the daemon said %q", lines.Text())
	}
	port, _ := strconv.Atoi(strings.TrimPrefix(lines.Text(), "port "))
	c := <-held
	defer c.Close()

	root := t.TempDir()
	interfacesList(t, root, port)
	env := rpcDropEnv{root: root, proc: "/proc", shut: func(_ context.Context, pid, fd int) error {
		if pid != cmd.Process.Pid {
			return fmt.Errorf("shut a socket of %d, not of the daemon %d", pid, cmd.Process.Pid)
		}
		return ShutdownForeignSocket(pid, fd)
	}}
	n, err := dropRPC(context.Background(), env, port, hang.Addr().String())
	if errors.Is(err, syscall.ENOSYS) || errors.Is(err, syscall.EPERM) {
		t.Skipf("pidfd_getfd is not available here: %v", err)
	}
	if err != nil || n != 1 {
		t.Fatalf("dropRPC: %d %v", n, err)
	}
	if !lines.Scan() || lines.Text() != "released" {
		t.Fatalf("the daemon said %q", lines.Text())
	}
	// the listener's end sees the connection end too
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Read(make([]byte, 64)); err == nil {
		// the request's bytes may still be buffered; the next read must end
		if _, err := c.Read(make([]byte, 64)); err == nil {
			t.Error("the listener's connection is still open")
		}
	}
}

// What the operation refuses, and that it touches only the daemon's connections to that listener.
func TestDropRPCChecks(t *testing.T) {
	root := t.TempDir()
	interfacesList(t, root, 32010)
	proc := filepath.Join(root, "proc")
	uid := os.Getuid()
	// the daemon's user listens on 32010 (IPv4-mapped, as Java does) and is connected to the
	// hanging 8199 twice and to the answering 8197; another user is connected to 8199 as well
	tcp6 := fmt.Sprintf(`  sl  local_address remote_address st tx_queue rx_queue tr tm->when retrnsmt uid timeout inode
   4: 0000000000000000FFFF00000100007F:7D0A 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000 %[1]d 0 10877 1 x 100 0 0 10 0
  16: 0000000000000000FFFF00000100007F:CD58 0000000000000000FFFF00000100007F:2007 01 00000000:00000000 00:00000000 00000000 %[1]d 0 77270 1 x 20 0 0 10 -1
  17: 0000000000000000FFFF00000100007F:CD60 0000000000000000FFFF00000100007F:2007 01 00000000:00000000 00:00000000 00000000 %[1]d 0 77271 1 x 20 0 0 10 -1
  18: 0000000000000000FFFF00000100007F:CD62 0000000000000000FFFF00000100007F:2005 01 00000000:00000000 00:00000000 00000000 %[1]d 0 77272 1 x 20 0 0 10 -1
  19: 0000000000000000FFFF00000100007F:CD64 0000000000000000FFFF00000100007F:2007 01 00000000:00000000 00:00000000 00000000 %[2]d 0 77273 1 x 20 0 0 10 -1
`, uid, uid+1)
	if err := os.MkdirAll(filepath.Join(proc, "net"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(proc, "net/tcp"), []byte("  sl header\n"), 0o644)
	_ = os.WriteFile(filepath.Join(proc, "net/tcp6"), []byte(tcp6), 0o644)
	fds := map[string]string{"10": "socket:[10877]", "11": "socket:[77270]", "12": "socket:[77271]", "13": "socket:[77272]", "14": "socket:[77273]", "3": "/dev/null"}
	fdDir := filepath.Join(proc, "2345", "fd")
	if err := os.MkdirAll(fdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for n, target := range fds {
		if err := os.Symlink(target, filepath.Join(fdDir, n)); err != nil {
			t.Fatal(err)
		}
	}
	var shut []string
	s := &Server{Policy: Policy{Root: root, ProcDir: proc}, rpcShut: func(_ context.Context, pid, fd int) error {
		shut = append(shut, fmt.Sprintf("%d/%d", pid, fd))
		return nil
	}}
	ctx := context.Background()
	for name, req := range map[string]request{
		"a port no interface has":  {Op: opRPCDrop, Args: []string{"2000", "127.0.0.1:8199"}},
		"the LAN interface's port": {Op: opRPCDrop, Args: []string{"2001", "127.0.0.1:8199"}},
		"not a port":               {Op: opRPCDrop, Args: []string{"x", "127.0.0.1:8199"}},
		"not a listener":           {Op: opRPCDrop, Args: []string{"32010", "localhost"}},
		"one argument":             {Op: opRPCDrop, Args: []string{"32010"}},
		"a path beside":            {Op: opRPCDrop, Args: []string{"32010", "127.0.0.1:8199"}, Path: "/etc/shadow"},
	} {
		if res := s.do(ctx, req); res.OK || !strings.HasPrefix(res.Error, "refused") {
			t.Errorf("%s: %+v", name, res)
		}
	}
	if len(shut) != 0 {
		t.Fatalf("a refused request shut %v", shut)
	}
	res := s.do(ctx, request{Op: opRPCDrop, Args: []string{"32010", "127.0.0.1:8199"}})
	if !res.OK || string(res.Stdout) != "2" {
		t.Fatalf("drop: %+v", res)
	}
	if strings.Join(shut, " ") != "2345/11 2345/12" && strings.Join(shut, " ") != "2345/12 2345/11" {
		t.Errorf("shut %v", shut)
	}
	// nothing connected to that listener: nothing to do, and that is not an error
	shut = nil
	if res := s.do(ctx, request{Op: opRPCDrop, Args: []string{"32010", "127.0.0.1:9999"}}); !res.OK || string(res.Stdout) != "0" || len(shut) != 0 {
		t.Errorf("none: %+v %v", res, shut)
	}
	// a filter that kills the child is said as such, not as a refusal
	s.rpcShut = func(context.Context, int, int) error { return ErrDropNotAllowed }
	if res := s.do(ctx, request{Op: opRPCDrop, Args: []string{"32010", "127.0.0.1:8199"}}); res.OK || res.Error != ErrDropNotAllowed.Error() {
		t.Errorf("not allowed: %+v", res)
	}
}
