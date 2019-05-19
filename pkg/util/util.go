package util

import (
	"strings"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/kubernetes/pkg/util/keymutex"
)

var (
	// serializes operations based on "volume name" as key
	VolumeNameMutex = keymutex.NewHashed(0)
)

// ValidateDriverName validates the driver name
func ValidateDriverName(driverName string) error {
	if len(driverName) == 0 {
		return errors.New("driver name is empty")
	}

	if len(driverName) > 63 {
		return errors.New("driver name length should be less than 63 chars")
	}
	var err error
	for _, msg := range validation.IsDNS1123Subdomain(strings.ToLower(driverName)) {
		if err == nil {
			err = errors.New(msg)
			continue
		}
		err = errors.Wrap(err, msg)
	}
	return err
}
