package util

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1 "github.com/openshift/api/config/v1"
)

const (
	infrastructureResourceName = "cluster"
)

func GetPlatform(ctx context.Context, infra configv1.Infrastructure) (configv1.PlatformType, error) {
	if infra.Status.PlatformStatus == nil {
		return "", nil
	}

	return infra.Status.PlatformStatus.Type, nil
}

func GetInfra(ctx context.Context, cl client.Reader) (configv1.Infrastructure, error) {
	infra := configv1.Infrastructure{}

	if err := cl.Get(ctx, client.ObjectKey{Name: infrastructureResourceName}, &infra); err != nil {
		return configv1.Infrastructure{}, fmt.Errorf("failed to get infrastructure %q: %w", infra.Kind, err)
	}

	return infra, nil
}
