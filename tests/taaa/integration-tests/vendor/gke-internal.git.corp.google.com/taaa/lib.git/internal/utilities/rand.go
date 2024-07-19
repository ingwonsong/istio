package utilities

import (
	"math/rand"
	"time"
)

var (
	R *rand.Rand
)

func init() {
	R = rand.New(rand.NewSource(time.Now().UnixNano()))
}
