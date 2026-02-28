package backend

import (
	"os"
	"path/filepath"
	"testing"

	"wick_server/wickfs"
)

func TestLocalBackend_FS(t *testing.T) {
	dir := t.TempDir()
	b := NewLocalBackend(dir, 30, 10_000)

	fs := b.FS()
	if fs == nil {
		t.Fatal("FS() returned nil")
	}
	if _, ok := fs.(*wickfs.LocalFS); !ok {
		t.Fatalf("expected *wickfs.LocalFS, got %T", fs)
	}
}

func TestLocalBackend_ID(t *testing.T) {
	b := NewLocalBackend(t.TempDir(), 30, 10_000)
	if b.ID() != "local" {
		t.Fatalf("expected 'local', got %q", b.ID())
	}
}

func TestLocalBackend_ContainerStatus(t *testing.T) {
	b := NewLocalBackend(t.TempDir(), 30, 10_000)
	if b.ContainerStatus() != "" {
		t.Fatalf("expected empty status, got %q", b.ContainerStatus())
	}
	if b.ContainerError() != "" {
		t.Fatalf("expected empty error, got %q", b.ContainerError())
	}
}

func TestLocalBackend_Execute(t *testing.T) {
	b := NewLocalBackend(t.TempDir(), 30, 10_000)

	t.Run("echo", func(t *testing.T) {
		resp := b.Execute("echo hello")
		if resp.ExitCode != 0 {
			t.Fatalf("expected exit 0, got %d: %s", resp.ExitCode, resp.Output)
		}
		if resp.Output != "hello\n" {
			t.Fatalf("expected 'hello\\n', got %q", resp.Output)
		}
	})

	t.Run("empty command", func(t *testing.T) {
		resp := b.Execute("")
		if resp.ExitCode != 1 {
			t.Fatalf("expected exit 1, got %d", resp.ExitCode)
		}
	})
}

func TestLocalBackend_ResolvePath(t *testing.T) {
	dir := t.TempDir()
	b := NewLocalBackend(dir, 30, 10_000)

	t.Run("relative", func(t *testing.T) {
		resolved, err := b.ResolvePath("foo/bar.txt")
		if err != nil {
			t.Fatal(err)
		}
		expected := filepath.Join(dir, "foo/bar.txt")
		if resolved != expected {
			t.Fatalf("expected %q, got %q", expected, resolved)
		}
	})

	t.Run("escape", func(t *testing.T) {
		_, err := b.ResolvePath("../../etc/passwd")
		if err == nil {
			t.Fatal("expected error for path escape")
		}
	})

	t.Run("empty", func(t *testing.T) {
		resolved, err := b.ResolvePath("")
		if err != nil {
			t.Fatal(err)
		}
		if resolved != dir {
			t.Fatalf("expected %q, got %q", dir, resolved)
		}
	})
}

func TestLocalBackend_UploadDownload(t *testing.T) {
	dir := t.TempDir()
	b := NewLocalBackend(dir, 30, 10_000)

	// Upload
	uploadResp := b.UploadFiles([]FileUpload{{Path: "test.txt", Content: []byte("hello")}})
	if len(uploadResp) != 1 || uploadResp[0].Error != "" {
		t.Fatalf("upload failed: %+v", uploadResp)
	}

	// Verify file exists
	data, err := os.ReadFile(filepath.Join(dir, "test.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatalf("expected 'hello', got %q", string(data))
	}

	// Download
	dlResp := b.DownloadFiles([]string{"test.txt"})
	if len(dlResp) != 1 || dlResp[0].Error != "" {
		t.Fatalf("download failed: %+v", dlResp)
	}
	if string(dlResp[0].Content) != "hello" {
		t.Fatalf("expected 'hello', got %q", string(dlResp[0].Content))
	}
}

func TestDockerBackend_FS(t *testing.T) {
	db := NewDockerBackend("test-container", "/workspace", 30, 10_000, "", "", "testuser")
	fs := db.FS()
	if fs == nil {
		t.Fatal("FS() returned nil")
	}
	if _, ok := fs.(*wickfs.RemoteFS); !ok {
		t.Fatalf("expected *wickfs.RemoteFS, got %T", fs)
	}
}

func TestDockerBackend_ContainerManager(t *testing.T) {
	db := NewDockerBackend("test-container", "/workspace", 30, 10_000, "", "", "testuser")

	// DockerBackend should implement ContainerManager
	var _ ContainerManager = db
}

func TestDockerBackend_Defaults(t *testing.T) {
	db := NewDockerBackend("", "", 0, 0, "", "", "user")
	if db.containerName != "wick-skills-sandbox" {
		t.Errorf("expected default container name, got %q", db.containerName)
	}
	if db.workdir != "/workspace" {
		t.Errorf("expected /workspace, got %q", db.workdir)
	}
	if db.image != "wick-sandbox" {
		t.Errorf("expected wick-sandbox, got %q", db.image)
	}
}
