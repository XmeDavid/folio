package auth

import (
	"bufio"
	_ "embed"
	"strings"
	"sync"

	"github.com/bits-and-blooms/bloom/v3"
)

//go:embed passwords_top10k.txt
var passwordsTop10k string

var (
	commonPasswordsOnce   sync.Once
	commonPasswordsFilter *bloom.BloomFilter
)

// IsCommonPassword reports whether password matches the embedded blocklist.
// Case-insensitive. Uses a bloom filter (~1% false-positive rate).
func IsCommonPassword(password string) bool {
	commonPasswordsOnce.Do(loadCommonPasswords)
	return commonPasswordsFilter.TestString(strings.ToLower(password))
}

func loadCommonPasswords() {
	commonPasswordsFilter = bloom.NewWithEstimates(10000, 0.01)
	scanner := bufio.NewScanner(strings.NewReader(passwordsTop10k))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		commonPasswordsFilter.AddString(strings.ToLower(line))
	}
}
