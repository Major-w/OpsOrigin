package vmware

import (
	"context"

	"github.com/opsorigin/core/internal/cloud"
)

type provider struct{}

func init() { cloud.Register(&provider{}) }

func (p *provider) Name() string { return "vmware" }

func (p *provider) ListResources(ctx context.Context, cred cloud.Credential, region string, kinds []cloud.ResourceType) ([]cloud.Resource, error) {
	return nil, nil
}

func (p *provider) GetResource(ctx context.Context, cred cloud.Credential, region, kind, id string) (*cloud.Resource, error) {
	return nil, &cloud.NoSuchResource{Provider: "vmware", ID: id}
}

func (p *provider) CollectMetrics(ctx context.Context, cred cloud.Credential, region string, resources []cloud.Resource) ([]cloud.Metric, error) {
	return nil, nil
}
