package ytdlp

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"

	ytdlp "github.com/lrstanley/go-ytdlp"
	"github.com/lrstanley/go-ytdlp/optiondata"

	"github.com/krau/SaveAny-Bot/config"
)

// buildDownloadCommand retains dev's defaults: any custom flags bypass format
// configuration, while output locations are kept in the task directory.
func buildDownloadCommand(cfg config.YtdlpConfig, dir string, flags []string) (*ytdlp.Command, []string, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to resolve download directory: %w", err)
	}
	rewritten, err := rewriteOutputFlags(flags, root)
	if err != nil {
		return nil, nil, err
	}
	cmd := ytdlp.New().Output(filepath.Join(root, "%(title)s.%(ext)s"))
	if len(flags) == 0 {
		cmd = applyFormatConfig(cmd, cfg)
	}
	return cmd, rewritten, nil
}

func rewriteOutputFlags(flags []string, root string) ([]string, error) {
	result := make([]string, 0, len(flags))
	for i := 0; i < len(flags); i++ {
		flag := flags[i]
		if flag == "--" {
			result = append(result, flags[i:]...)
			break
		}
		leading, normalized := normalizeOutputOption(flag)
		result = append(result, leading...)
		flag = normalized
		var option, value, form string
		switch {
		case flag == "-o" || flag == "--output" || flag == "-P" || flag == "--paths":
			option, form = flag, "separate"
			if i+1 == len(flags) || strings.HasPrefix(flags[i+1], "-") {
				return nil, fmt.Errorf("%s requires a relative output value", flag)
			}
			i++
			value = flags[i]
		case strings.HasPrefix(flag, "--output="):
			option, value, form = "--output", strings.TrimPrefix(flag, "--output="), "equals"
		case strings.HasPrefix(flag, "--paths="):
			option, value, form = "--paths", strings.TrimPrefix(flag, "--paths="), "equals"
		case strings.HasPrefix(flag, "-o") && len(flag) > 2:
			option, value, form = "-o", flag[2:], "attached"
		case strings.HasPrefix(flag, "-P") && len(flag) > 2:
			option, value, form = "-P", flag[2:], "attached"
		default:
			result = append(result, flag)
			// Option values can themselves start with -o or -P. Use the pinned
			// wrapper's metadata to avoid mistaking them for output options.
			count := min(remainingOptionArguments(flag), len(flags)-i-1)
			result = append(result, flags[i+1:i+1+count]...)
			i += count
			continue
		}
		isPath := option == "-P" || option == "--paths"
		resolved, err := rootOutputValue(root, value, isPath)
		if err != nil {
			return nil, fmt.Errorf("invalid %s value: %w", option, err)
		}
		switch form {
		case "separate":
			result = append(result, option, resolved)
		case "equals":
			result = append(result, option+"="+resolved)
		case "attached":
			result = append(result, option+resolved)
		}
	}
	return result, nil
}

var optionArguments = func() map[string]int {
	args := make(map[string]int)
	for _, group := range optiondata.Groups {
		for _, option := range group.Options {
			for _, flag := range option.LongFlags {
				args[flag] = option.NArgs
			}
			for _, flag := range option.ShortFlags {
				args[flag] = option.NArgs
			}
		}
	}
	return args
}()

func canonicalOption(name string) string {
	if _, ok := optionArguments[name]; ok || !strings.HasPrefix(name, "--") {
		return name
	}
	match := ""
	for flag := range optionArguments {
		if strings.HasPrefix(flag, name) {
			if match != "" {
				return name
			} // Leave ambiguous options to yt-dlp.
			match = flag
		}
	}
	if match != "" {
		return match
	}
	return name
}

func remainingOptionArguments(flag string) int {
	if strings.HasPrefix(flag, "--") {
		name, _, attached := strings.Cut(flag, "=")
		count := optionArguments[canonicalOption(name)]
		if attached {
			return max(count-1, 0)
		}
		return count
	}
	if count, known := optionArguments[flag]; known {
		return count
	}
	if strings.HasPrefix(flag, "-") {
		for i := 1; i < len(flag); i++ {
			count, known := optionArguments["-"+flag[i:i+1]]
			if !known {
				return 0
			}
			if count > 0 {
				if i+1 < len(flag) {
					return max(count-1, 0)
				}
				return count
			}
		}
	}
	return 0
}

func normalizeOutputOption(flag string) ([]string, string) {
	if strings.HasPrefix(flag, "--") {
		name, value, equals := strings.Cut(flag, "=")
		canonical := canonicalOption(name)
		if canonical == "--output" || canonical == "--paths" {
			if equals {
				return nil, canonical + "=" + value
			}
			return nil, canonical
		}
		return nil, flag
	}
	if strings.HasPrefix(flag, "-") && len(flag) > 2 {
		for i := 1; i < len(flag); i++ {
			name := "-" + flag[i:i+1]
			count, known := optionArguments[name]
			if !known {
				break
			}
			if name == "-o" || name == "-P" {
				var leading []string
				for j := 1; j < i; j++ {
					leading = append(leading, "-"+flag[j:j+1])
				}
				return leading, name + flag[i+1:]
			}
			if count > 0 {
				break
			} // Remaining characters belong to this value.
		}
	}
	return nil, flag
}

var outputTypes = map[string]bool{
	"annotation": true, "chapter": true, "description": true, "infojson": true,
	"link": true, "pl_description": true, "pl_infojson": true, "pl_thumbnail": true,
	"pl_video": true, "subtitle": true, "thumbnail": true,
}

func rootOutputValue(root, value string, directory bool) (string, error) {
	prefix, relative := "", value
	if types, rest, ok := strings.Cut(value, ":"); ok {
		known := true
		for _, kind := range strings.Split(types, "+") {
			if !outputTypes[kind] && !(directory && (kind == "home" || kind == "temp")) {
				known = false
				break
			}
		}
		if known {
			prefix, relative = types+":", rest
		}
	}
	// Empty typed templates disable sidecar output in yt-dlp.
	if relative == "" && prefix != "" && !directory {
		return prefix, nil
	}
	// Treat both separator styles consistently on Windows and Linux. Reject
	// drive/stream syntax too, rather than interpreting it differently by OS.
	relative = strings.ReplaceAll(relative, "\\", "/")
	if relative == "" || relative == "-" || strings.HasPrefix(relative, "/") || hasPathColon(relative, directory) {
		return "", fmt.Errorf("output must be a relative path: %q", value)
	}
	output := filepath.Join(root, filepath.FromSlash(relative))
	rel, err := filepath.Rel(root, output)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || (rel == "." && !directory) {
		return "", fmt.Errorf("output escapes the download directory or names a directory: %q", value)
	}
	return prefix + output, nil
}

// Colons inside output fields (for example date formatting) are template
// syntax. Only literal path colons can introduce drives or Windows streams.
func hasPathColon(value string, directory bool) bool {
	if directory {
		return strings.Contains(value, ":")
	}
	for {
		before, after, found := strings.Cut(value, "%(")
		if strings.Contains(before, ":") {
			return true
		}
		if !found {
			return false
		}
		_, rest, closed := strings.Cut(after, ")")
		if !closed {
			return strings.Contains(after, ":")
		}
		value = rest
	}
}

func collectDownloadedFiles(ctx context.Context, root string) ([]string, error) {
	var files []string
	names := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("download is not a regular file: %s", path)
		}
		// Storage paths still use dev's flat filenames. Detect collisions before
		// uploading anything instead of silently overwriting files in subfolders.
		name := sanitizeFilename(filepath.Base(path))
		key := name
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if previous, exists := names[key]; exists {
			return fmt.Errorf("downloads %s and %s have the same storage filename %q", previous, path, name)
		}
		names[key] = path
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to collect downloaded files: %w", err)
	}
	return files, nil
}
