package models

import (
	"fmt"

	"github.com/google/uuid"
)

const (
	ClusterPrefix              = "cls"
	NamespacePrefix            = "wrk"
	ClusterAccessSessionPrefix = "cas"
)

func NewClusterID() string {
	return fmt.Sprintf("%s_%s", ClusterPrefix, uuid.New().String())
}

func NewNamespaceID() string {
	return fmt.Sprintf("%s_%s", NamespacePrefix, uuid.New().String())
}

func NewClusterAccessSessionID() string {
	return fmt.Sprintf("%s_%s", ClusterAccessSessionPrefix, uuid.New().String())
}
