package ytdlp

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/pkg/enums/ctxkey"
)

func TestRewriteOutputFlags(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "sub", "%(id)s.%(ext)s")
	for _, tc := range []struct {
		name        string
		flags, want []string
	}{
		{"short", []string{"-o", "sub/%(id)s.%(ext)s"}, []string{"-o", file}},
		{"long", []string{"--output", "sub/%(id)s.%(ext)s"}, []string{"--output", file}},
		{"equals", []string{"--output=sub/%(id)s.%(ext)s"}, []string{"--output=" + file}},
		{"attached", []string{"-osub/%(id)s.%(ext)s"}, []string{"-o" + file}},
		{"combined short flags", []string{"-xvosub/%(id)s.%(ext)s"}, []string{"-x", "-v", "-o" + file}},
		{"combined separate value", []string{"-xo", "sub/%(id)s.%(ext)s"}, []string{"-x", "-o", file}},
		{"attached format value", []string{"-f-original"}, []string{"-f-original"}},
		{"combined format operand", []string{"-xf", "-original"}, []string{"-xf", "-original"}},
		{"multi-value equals operand", []string{"--print-to-file=-o", "-original.txt"}, []string{"--print-to-file=-o", "-original.txt"}},
		{"abbreviated paths", []string{"--path=temp:sub"}, []string{"--paths=temp:" + filepath.Join(root, "sub")}},
		{"repeated", []string{"-o", "first.mp4", "--output=sub/%(id)s.%(ext)s"}, []string{"-o", filepath.Join(root, "first.mp4"), "--output=" + file}},
		{"typed", []string{"-o", "subtitle+thumbnail:sub/%(id)s.%(ext)s"}, []string{"-o", "subtitle+thumbnail:" + file}},
		{"disabled sidecar", []string{"-o", "subtitle:"}, []string{"-o", "subtitle:"}},
		{"field date format", []string{"-o", "%(upload_date>%Y:%m:%d)s.mp4"}, []string{"-o", filepath.Join(root, "%(upload_date>%Y:%m:%d)s.mp4")}},
		{"paths", []string{"-P", "temp:sub", "--paths=home:.", "-Pthumbnail:sub"}, []string{"-P", "temp:" + filepath.Join(root, "sub"), "--paths=home:" + root, "-Pthumbnail:" + filepath.Join(root, "sub")}},
		{"windows separators", []string{"-o", `sub\%(id)s.%(ext)s`}, []string{"-o", file}},
		{"other options", []string{"-f", "best", "--output-na-placeholder", "none", "-O", "%(id)s"}, []string{"-f", "best", "--output-na-placeholder", "none", "-O", "%(id)s"}},
		{"option values resembling flags", []string{"--match-title", "-original", "--print-to-file", "-o", "report.txt", "--exec", "-Program"}, []string{"--match-title", "-original", "--print-to-file", "-o", "report.txt", "--exec", "-Program"}},
		{"end options", []string{"--", "-oignored"}, []string{"--", "-oignored"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := append([]string(nil), tc.flags...)
			got, err := rewriteOutputFlags(tc.flags, root)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got=%q want=%q err=%v", got, tc.want, err)
			}
			if !reflect.DeepEqual(tc.flags, original) {
				t.Fatal("mutated original flags")
			}
		})
	}
}

func TestRewriteOutputFlagsRejectsInvalidValues(t *testing.T) {
	for _, flags := range [][]string{
		{"-o"}, {"--paths"}, {"--output="}, {"--paths="}, {"-o", "--write-sub"},
		{"-o", "-"}, {"-o", "../escape.mp4"}, {"-o", `..\escape.mp4`},
		{"-o", "sub/../../escape.mp4"}, {"-o", "/absolute.mp4"}, {"-o", `C:\absolute.mp4`},
		{"-o", `\\host\share\file.mp4`}, {"-o", "sub/.."}, {"--paths", "temp:../escape"}, {"--paths", "temp:"},
	} {
		t.Run(flags[len(flags)-1], func(t *testing.T) {
			if _, err := rewriteOutputFlags(flags, t.TempDir()); err == nil {
				t.Fatalf("accepted %q", flags)
			}
		})
	}
}

func TestDownloadCommandPreservesDevDefaults(t *testing.T) {
	cfg := config.YtdlpConfig{MaxHeight: 720, Recode: "mp4"}
	for _, flags := range [][]string{nil, {"--write-sub"}, {"-o", "sub/%(id)s.%(ext)s"}, {"-f", "best"}} {
		cmd, _, err := buildDownloadCommand(cfg, t.TempDir(), flags)
		if err != nil {
			t.Fatal(err)
		}
		f := cmd.GetFlagConfig()
		if len(flags) == 0 {
			if f.VideoFormat.Format == nil || *f.VideoFormat.Format != buildFormatSelector(720) || f.Filesystem.RestrictFilenames == nil || !*f.Filesystem.RestrictFilenames || f.PostProcessing.RecodeVideo == nil || *f.PostProcessing.RecodeVideo != "mp4" {
				t.Fatal("dev defaults changed")
			}
		} else if f.VideoFormat.Format != nil || f.VideoFormat.FormatSort != nil || f.PostProcessing.RecodeVideo != nil || f.Filesystem.RestrictFilenames != nil {
			t.Fatal("custom flags no longer bypass all format defaults")
		}
		if f.Filesystem.Output == nil || !filepath.IsAbs(*f.Filesystem.Output) || filepath.Base(*f.Filesystem.Output) != "%(title)s.%(ext)s" {
			t.Fatal("default output template changed")
		}
	}
}

func TestCollectDownloadedFilesRecursesAndRejectsCollisions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		paths   []string
		wantErr bool
	}{
		{"nested", []string{"first.mp4", "sub/second.mp4", "sub/deep/third.srt"}, false},
		{"duplicate basename", []string{"one/video.mp4", "two/video.mp4"}, true},
		{"sanitized collision", []string{"one/a\"b.mp4", "two/a'b.mp4"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Quotes are not legal in Windows filenames; cover sanitization below.
			if tc.name == "sanitized collision" && os.PathSeparator == '\\' {
				t.Skip("Windows does not allow quotes in filenames")
			}
			root := t.TempDir()
			for _, path := range tc.paths {
				path = filepath.Join(root, filepath.FromSlash(path))
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("content"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			files, err := collectDownloadedFiles(context.Background(), root)
			if tc.wantErr {
				if err == nil {
					t.Fatal("colliding downloads accepted")
				}
				return
			}
			if err != nil || len(files) != len(tc.paths) {
				t.Fatalf("files=%v error=%v", files, err)
			}
			for _, path := range files {
				if _, err := os.ReadFile(path); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := collectDownloadedFiles(ctx, root); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
		})
	}
}

type recordingStorage struct {
	MockStorage
	path    string
	content []byte
	length  int64
}

func (s *recordingStorage) Save(ctx context.Context, r io.Reader, path string) error {
	s.path = path
	s.length, _ = ctx.Value(ctxkey.ContentLength).(int64)
	var err error
	s.content, err = io.ReadAll(r)
	return err
}

func TestNestedDownloadPreservesFlatStorageDestination(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "nested", "video.mp4")
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("payload"), 0600); err != nil {
		t.Fatal(err)
	}
	files, err := collectDownloadedFiles(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	stor := &recordingStorage{}
	task := &Task{Storage: stor, StorPath: "destination"}
	if err := task.transferFile(context.Background(), files[0]); err != nil {
		t.Fatal(err)
	}
	if stor.path != filepath.Join("destination", "video.mp4") || string(stor.content) != "payload" || stor.length != 7 {
		t.Fatalf("destination=%q content=%q length=%d", stor.path, stor.content, stor.length)
	}
}

func TestCollectDownloadedFilesRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.mp4")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.mp4")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := collectDownloadedFiles(context.Background(), root); err == nil {
		t.Fatal("accepted symlink outside task directory")
	}
}
