package encryption

type AEAD interface {
	Open(string) (string, error)
	Seal(string) (string, error)
}
