package wecomaibot

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

func generateReqID(cmd string) string {
	now := time.Now().UnixNano()
	rb := make([]byte, 4)
	_, _ = rand.Read(rb)
	return fmt.Sprintf("%s_%d_%s", cmd, now, hex.EncodeToString(rb))
}
