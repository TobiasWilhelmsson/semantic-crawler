package semantic

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestScanLockProcessHelper(t *testing.T) {
	path := os.Getenv("SEMANTIC_TEST_SCAN_LOCK_PATH")
	if path == "" {
		t.Skip("subprocess helper")
	}
	lease, err := AcquireScanLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	fmt.Println("locked")
	time.Sleep(30 * time.Second)
}
func TestScanLockRejectsExternalProcessAndRecoversAfterExit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.db")
	child := exec.Command(os.Args[0], "-test.run=^TestScanLockProcessHelper$")
	child.Env = append(os.Environ(), "SEMANTIC_TEST_SCAN_LOCK_PATH="+path)
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if child.ProcessState == nil {
			child.Process.Kill()
			child.Wait()
		}
	}()
	ready, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || ready != "locked\n" {
		t.Fatalf("lock helper: %q %v", ready, err)
	}
	if lease, err := AcquireScanLock(path); !errors.Is(err, ErrScanBusy) {
		if lease != nil {
			lease.Close()
		}
		t.Fatalf("external owner bypassed: %v", err)
	}
	if err = child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	child.Wait()
	lease, err := AcquireScanLock(path)
	if err != nil {
		t.Fatal(err)
	}
	lease.Close()
}
