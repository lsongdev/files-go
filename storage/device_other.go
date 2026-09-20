//go:build !linux

package storage

import "fmt"

func configuredDeviceIdentity(root, uuid string) (uint64, error) {
	if uuid != "" {
		return 0, fmt.Errorf("device_uuid is only supported on linux")
	}
	return rootDeviceIdentity(root)
}
