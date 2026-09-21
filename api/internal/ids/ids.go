package ids

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

func New(prefix string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(b[:]))
}
