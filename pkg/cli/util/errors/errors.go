package errors

import (
	"fmt"

	errutil "github.com/faroshq/faros/pkg/util/error"
)

func ParseCloudError(err error) error {
	isCloudError, cErr := errutil.IsCloudError(err)
	if isCloudError {
		return fmt.Errorf(cErr.CloudErrorBody.Message)
	}
	return err
}
