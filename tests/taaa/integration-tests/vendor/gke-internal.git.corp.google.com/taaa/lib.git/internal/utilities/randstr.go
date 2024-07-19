package utilities

const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// RandStr returns random alphanumeric string of length length.
func RandStr(length int) string {
	buf := make([]byte, length)
	for i := range buf {
		buf[i] = chars[R.Intn(len(chars))]
	}
	return string(buf)
}
