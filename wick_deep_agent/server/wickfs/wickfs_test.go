package wickfs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalFS_Ls(t *testing.T) {
	fs := NewLocalFS()
	ctx := context.Background()

	t.Run("empty dir", func(t *testing.T) {
		dir := t.TempDir()
		entries, err := fs.Ls(ctx, dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("expected 0 entries, got %d", len(entries))
		}
	})

	t.Run("with files and dirs", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hi"), 0644)
		os.Mkdir(filepath.Join(dir, "subdir"), 0755)

		entries, err := fs.Ls(ctx, dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 2 {
			t.Fatalf("expected 2 entries, got %d", len(entries))
		}

		found := map[string]string{}
		for _, e := range entries {
			found[e.Name] = e.Type
		}
		if found["hello.txt"] != "file" {
			t.Errorf("expected hello.txt as file, got %q", found["hello.txt"])
		}
		if found["subdir"] != "dir" {
			t.Errorf("expected subdir as dir, got %q", found["subdir"])
		}
	})

	t.Run("nonexistent path", func(t *testing.T) {
		_, err := fs.Ls(ctx, "/nonexistent-path-12345")
		if err == nil {
			t.Fatal("expected error for nonexistent path")
		}
	})
}

func TestLocalFS_ReadFile(t *testing.T) {
	fs := NewLocalFS()
	ctx := context.Background()

	t.Run("utf8 content", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.txt")
		os.WriteFile(path, []byte("hello world"), 0644)

		content, err := fs.ReadFile(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		if content != "hello world" {
			t.Fatalf("expected 'hello world', got %q", content)
		}
	})

	t.Run("binary content", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.bin")
		os.WriteFile(path, []byte{0xff, 0xfe, 0x00, 0x01}, 0644)

		content, err := fs.ReadFile(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		if content[:7] != "base64:" {
			t.Fatalf("expected base64: prefix, got %q", content[:7])
		}
	})

	t.Run("nonexistent file", func(t *testing.T) {
		_, err := fs.ReadFile(ctx, "/nonexistent-file-12345")
		if err == nil {
			t.Fatal("expected error for nonexistent file")
		}
	})
}

func TestLocalFS_WriteFile(t *testing.T) {
	fs := NewLocalFS()
	ctx := context.Background()

	t.Run("basic write", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "out.txt")

		result, err := fs.WriteFile(ctx, path, "hello")
		if err != nil {
			t.Fatal(err)
		}
		if result.BytesWritten != 5 {
			t.Fatalf("expected 5 bytes, got %d", result.BytesWritten)
		}

		data, _ := os.ReadFile(path)
		if string(data) != "hello" {
			t.Fatalf("expected 'hello', got %q", string(data))
		}
	})

	t.Run("creates parent dirs", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "a", "b", "c.txt")

		_, err := fs.WriteFile(ctx, path, "nested")
		if err != nil {
			t.Fatal(err)
		}

		data, _ := os.ReadFile(path)
		if string(data) != "nested" {
			t.Fatalf("expected 'nested', got %q", string(data))
		}
	})
}

func TestLocalFS_EditFile(t *testing.T) {
	fs := NewLocalFS()
	ctx := context.Background()

	t.Run("successful edit", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "edit.txt")
		os.WriteFile(path, []byte("hello world"), 0644)

		result, err := fs.EditFile(ctx, path, "world", "go")
		if err != nil {
			t.Fatal(err)
		}
		if result.Replacements != 1 {
			t.Fatalf("expected 1 replacement, got %d", result.Replacements)
		}

		data, _ := os.ReadFile(path)
		if string(data) != "hello go" {
			t.Fatalf("expected 'hello go', got %q", string(data))
		}
	})

	t.Run("old_text not found", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "edit2.txt")
		os.WriteFile(path, []byte("hello"), 0644)

		_, err := fs.EditFile(ctx, path, "xyz", "abc")
		if err == nil {
			t.Fatal("expected error for old_text not found")
		}
	})

	t.Run("empty old_text", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "edit3.txt")
		os.WriteFile(path, []byte("hello"), 0644)

		_, err := fs.EditFile(ctx, path, "", "abc")
		if err == nil {
			t.Fatal("expected error for empty old_text")
		}
	})
}

func TestLocalFS_Grep(t *testing.T) {
	fs := NewLocalFS()
	ctx := context.Background()

	t.Run("matches found", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "a.txt"), []byte("line1 foo\nline2 bar\nline3 foo"), 0644)

		result, err := fs.Grep(ctx, "foo", dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Matches) != 2 {
			t.Fatalf("expected 2 matches, got %d", len(result.Matches))
		}
		if result.Matches[0].Line != 1 {
			t.Errorf("expected line 1, got %d", result.Matches[0].Line)
		}
		if result.Matches[1].Line != 3 {
			t.Errorf("expected line 3, got %d", result.Matches[1].Line)
		}
	})

	t.Run("no matches", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0644)

		result, err := fs.Grep(ctx, "xyz", dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Matches) != 0 {
			t.Fatalf("expected 0 matches, got %d", len(result.Matches))
		}
	})

	t.Run("invalid regex", func(t *testing.T) {
		dir := t.TempDir()
		_, err := fs.Grep(ctx, "[invalid", dir)
		if err == nil {
			t.Fatal("expected error for invalid regex")
		}
	})

	t.Run("skips hidden dirs", func(t *testing.T) {
		dir := t.TempDir()
		hidden := filepath.Join(dir, ".hidden")
		os.Mkdir(hidden, 0755)
		os.WriteFile(filepath.Join(hidden, "a.txt"), []byte("match_me"), 0644)
		os.WriteFile(filepath.Join(dir, "b.txt"), []byte("match_me"), 0644)

		result, err := fs.Grep(ctx, "match_me", dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Matches) != 1 {
			t.Fatalf("expected 1 match (hidden dir skipped), got %d", len(result.Matches))
		}
	})
}

func TestLocalFS_Glob(t *testing.T) {
	fs := NewLocalFS()
	ctx := context.Background()

	t.Run("pattern matching", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "a.go"), []byte(""), 0644)
		os.WriteFile(filepath.Join(dir, "b.go"), []byte(""), 0644)
		os.WriteFile(filepath.Join(dir, "c.txt"), []byte(""), 0644)

		result, err := fs.Glob(ctx, "*.go", dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Files) != 2 {
			t.Fatalf("expected 2 files, got %d", len(result.Files))
		}
	})

	t.Run("no matches", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "a.txt"), []byte(""), 0644)

		result, err := fs.Glob(ctx, "*.go", dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Files) != 0 {
			t.Fatalf("expected 0 files, got %d", len(result.Files))
		}
	})
}

func TestLocalFS_Exec(t *testing.T) {
	fs := NewLocalFS()
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		result, err := fs.Exec(ctx, "echo hello")
		if err != nil {
			t.Fatal(err)
		}
		if result.ExitCode != 0 {
			t.Fatalf("expected exit code 0, got %d", result.ExitCode)
		}
		if result.Stdout != "hello\n" {
			t.Fatalf("expected 'hello\\n', got %q", result.Stdout)
		}
	})

	t.Run("nonzero exit", func(t *testing.T) {
		result, err := fs.Exec(ctx, "exit 42")
		if err != nil {
			t.Fatal(err)
		}
		if result.ExitCode != 42 {
			t.Fatalf("expected exit code 42, got %d", result.ExitCode)
		}
	})
}
