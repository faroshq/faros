package strings

import (
	"math/rand"
	"strings"
	"time"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

var chars = []rune("abcdefghijklmnopqrstuvwxyz")

// RandomString return random string
func RandomString(length int) string {
	var b strings.Builder
	for i := 0; i < length; i++ {
		b.WriteRune(chars[rand.Intn(len(chars))])
	}
	return b.String()
}
