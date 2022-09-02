package models

import (
	"fmt"

	"github.com/google/uuid"
)

const (
	UserPrefix                 = "usr"
	ClusterPrefix              = "cls"
	NamespacePrefix            = "nms"
	ClusterAccessSessionPrefix = "cas"
)

func NewUserID() string {
	return fmt.Sprintf("%s_%s", UserPrefix, uuid.New().String())
}

func NewClusterID() string {
	return fmt.Sprintf("%s_%s", ClusterPrefix, uuid.New().String())
}

func NewNamespaceID() string {
	return fmt.Sprintf("%s_%s", NamespacePrefix, uuid.New().String())
}

func NewClusterAccessSessionID() string {
	return fmt.Sprintf("%s_%s", ClusterAccessSessionPrefix, uuid.New().String())
}
