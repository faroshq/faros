package cloud

import (
	"context"

	"github.com/faroshq/faros/pkg/models"
)

type Cloud interface {
	ListClusters(context.Context) ([]models.Cluster, error)

	Run(context.Context) error
}
