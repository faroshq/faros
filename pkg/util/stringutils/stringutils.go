package stringutils

import (
	"strings"

	"github.com/docker/docker/pkg/namesgenerator"
)

func LastTokenByte(s string, sep byte) string {
	return s[strings.LastIndexByte(s, sep)+1:]
}

func GetRandomName() string {
	return strings.ReplaceAll(namesgenerator.GetRandomName(0), "_", "-")
}
