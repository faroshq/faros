//go:build e2e
// +build e2e

package e2e

import (
	"math/rand"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/onsi/gomega/format"
)

func TestE2E(t *testing.T) {
	rand.Seed(time.Now().UnixNano())

	RegisterFailHandler(Fail)
	format.TruncatedDiff = false
	testingT = t
	RunSpecs(t, "e2e tests")
}
