package updater

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
)

func (c *client) assetEndpoint(r *Release, a asset) string {
	return fmt.Sprintf("%s/repos/%s/releases/assets/%d", c.baseURL, r.repository, a.ID)
}

func (c *client) checksum(ctx context.Context, r *Release) ([]byte, error) {
	if err := r.VerificationError(); err != nil {
		return nil, err
	}
	digest := r.asset.Digest
	if digest != "" {
		if !strings.HasPrefix(digest, "sha256:") {
			return nil, errors.New("unsupported asset digest")
		}
		digest = strings.TrimPrefix(digest, "sha256:")
	} else {
		if r.checksumAsset == nil {
			return nil, errors.New("release has no SHA-256 digest or checksum; update manually")
		}
		resp, err := c.get(ctx, c.assetEndpoint(r, *r.checksumAsset), "application/octet-stream")
		if err != nil {
			return nil, err
		}
		data, err := readResponse(resp, 4096)
		if err != nil {
			return nil, err
		}
		if int64(len(data)) != r.checksumAsset.Size {
			return nil, errors.New("checksum asset size mismatch")
		}
		fields := strings.Fields(string(data))
		if len(fields) != 1 && !(len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == r.asset.Name) {
			return nil, errors.New("invalid checksum file")
		}
		digest = fields[0]
	}
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != sha256.Size {
		return nil, errors.New("invalid SHA-256 digest")
	}
	return decoded, nil
}

func (c *client) download(ctx context.Context, r *Release, target *os.File, expected []byte) error {
	resp, err := c.get(ctx, c.assetEndpoint(r, r.asset), "application/octet-stream")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("asset download returned HTTP %d", resp.StatusCode)
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(target, hash), io.LimitReader(resp.Body, r.asset.Size+1))
	if err != nil {
		return fmt.Errorf("download asset: %w", err)
	}
	if n != r.asset.Size {
		return errors.New("release asset size mismatch")
	}
	if !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), hex.EncodeToString(expected)) {
		return errors.New("release asset SHA-256 mismatch")
	}
	_, err = target.Seek(0, io.SeekStart)
	return err
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func copyBinary(ctx context.Context, dest io.Writer, src io.Reader) error {
	n, err := io.Copy(dest, io.LimitReader(contextReader{ctx, src}, maxAssetBytes+1))
	if err != nil {
		return err
	}
	if n == 0 || n > maxAssetBytes {
		return errors.New("invalid executable size")
	}
	return ctx.Err()
}

func executableEntry(name, command string) bool {
	// No archive paths are ever written, but reject traversal and special entries.
	return !strings.Contains(name, "\\") && !strings.HasPrefix(name, "/") && path.Clean(name) == name && name != ".." && !strings.HasPrefix(name, "../") && path.Base(name) == command
}

func (c *client) extract(ctx context.Context, source *os.File, name string, dest io.Writer) error {
	command := "saveany-bot"
	if c.goos == "windows" {
		command += ".exe"
	}
	if strings.HasSuffix(name, ".zip") {
		info, err := source.Stat()
		if err != nil {
			return err
		}
		archive, err := zip.NewReader(source, info.Size())
		if err != nil {
			return err
		}
		var selected *zip.File
		for _, entry := range archive.File {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !executableEntry(entry.Name, command) {
				continue
			}
			if selected != nil || !entry.Mode().IsRegular() || entry.UncompressedSize64 > maxAssetBytes {
				return errors.New("invalid or ambiguous executable in zip")
			}
			selected = entry
		}
		if selected == nil {
			return errors.New("executable not found in zip")
		}
		reader, err := selected.Open()
		if err != nil {
			return err
		}
		err = copyBinary(ctx, dest, reader)
		return errors.Join(err, reader.Close())
	}
	if strings.HasSuffix(name, ".tar.gz") {
		reader, err := gzip.NewReader(contextReader{ctx, source})
		if err != nil {
			return err
		}
		defer reader.Close()
		limited := &io.LimitedReader{R: contextReader{ctx, reader}, N: maxAssetBytes + 1}
		archive := tar.NewReader(limited)
		found := false
		for {
			entry, err := archive.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return err
			}
			if !executableEntry(entry.Name, command) {
				continue
			}
			if found || (entry.Typeflag != tar.TypeReg && entry.Typeflag != tar.TypeRegA) || entry.Size > maxAssetBytes {
				return errors.New("invalid or ambiguous executable in tar")
			}
			if err := copyBinary(ctx, dest, archive); err != nil {
				return err
			}
			found = true
		}
		// Read through the gzip footer too; Close alone does not check its CRC.
		if _, err := io.Copy(io.Discard, limited); err != nil {
			return err
		}
		if limited.N <= 0 {
			return errors.New("uncompressed archive exceeds size limit")
		}
		if !found {
			return errors.New("executable not found in tar")
		}
		return ctx.Err()
	}
	return copyBinary(ctx, dest, source)
}
