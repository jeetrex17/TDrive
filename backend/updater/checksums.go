package updater

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"io"
	"path"
	"strings"
)

// parseChecksums reads sha256sum output ("<hex>  <name>" or "<hex> *<name>")
// into a map keyed by bare file name. Paths are reduced to their base name so
// a manifest generated as "dist/TDrive-..." still matches the asset.
func parseChecksums(r io.Reader) (map[string]string, error) {
	sums := make(map[string]string)
	scanner := bufio.NewScanner(r)
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		fields := strings.Fields(text)
		if len(fields) < 2 {
			return nil, fmt.Errorf("checksums: line %d is malformed", line)
		}
		sum := strings.ToLower(fields[0])
		if len(sum) != 64 {
			return nil, fmt.Errorf("checksums: line %d has a malformed digest", line)
		}
		if _, err := hex.DecodeString(sum); err != nil {
			return nil, fmt.Errorf("checksums: line %d has a malformed digest", line)
		}
		name := strings.TrimPrefix(strings.Join(fields[1:], " "), "*")
		name = path.Base(strings.ReplaceAll(name, "\\", "/"))
		if name == "" || name == "." || name == "/" {
			return nil, fmt.Errorf("checksums: line %d has no file name", line)
		}
		sums[name] = sum
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("checksums: %w", err)
	}
	return sums, nil
}
