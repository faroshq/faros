package encryption

//go:generate rm -rf ../mocks/$GOPACKAGE
//go:generate go run ../../../vendor/github.com/golang/mock/mockgen -destination=../mocks/$GOPACKAGE/$GOPACKAGE.go github.com/faroshq/faros/pkg/util/$GOPACKAGE AEAD
//go:generate go run ../../../vendor/golang.org/x/tools/cmd/goimports -local=github.com/faroshq/faros -e -w ../mocks/$GOPACKAGE/$GOPACKAGE.go
