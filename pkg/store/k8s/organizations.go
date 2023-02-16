package k8s

import (
	"context"
	"crypto/sha256"
	"strings"

	"github.com/martinlindhe/base36"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
	"github.com/faroshq/faros/pkg/models"
)

func (s *store) GetOrganization(ctx context.Context, organization tenancyv1alpha1.Organization) (*tenancyv1alpha1.Organization, error) {
	if organization.Labels == nil {
		organization.Labels = make(map[string]string)
	}

	current, err := s.farosclientset.TenancyV1alpha1().Organizations().Get(ctx, getOrganizationID(&organization), metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	current.Name = current.Labels[models.LabelOrganization]
	current.Labels[models.LabelOrganizationHash] = getOrganizationID(&organization)

	return current, nil
}

func (s *store) ListOrganizations(ctx context.Context, organization tenancyv1alpha1.Organization) (*tenancyv1alpha1.OrganizationList, error) {
	organizations, err := s.farosclientset.TenancyV1alpha1().Organizations().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for idx, organization := range organizations.Items {
		organizations.Items[idx].Name = organization.Labels[models.LabelOrganization]
	}
	return organizations, nil
}

func (s *store) DeleteOrganization(ctx context.Context, organization tenancyv1alpha1.Organization) error {
	return s.farosclientset.TenancyV1alpha1().Organizations().Delete(ctx, getOrganizationID(&organization), metav1.DeleteOptions{})
}

func (s *store) CreateOrganization(ctx context.Context, organization tenancyv1alpha1.Organization) (*tenancyv1alpha1.Organization, error) {
	if organization.Labels == nil {
		organization.Labels = make(map[string]string)
	}
	organization.Labels[models.LabelOrganization] = organization.Name
	organization.Name = getOrganizationID(&organization)
	return s.farosclientset.TenancyV1alpha1().Organizations().Create(ctx, &organization, metav1.CreateOptions{})
}

func (s *store) UpdateOrganization(ctx context.Context, organization tenancyv1alpha1.Organization) (*tenancyv1alpha1.Organization, error) {
	if organization.Labels == nil {
		organization.Labels = make(map[string]string)
	}
	organization.Labels[models.LabelOrganization] = organization.Name
	organization.Name = getOrganizationID(&organization)

	current, err := s.farosclientset.TenancyV1alpha1().Organizations().Get(ctx, getOrganizationID(&organization), metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	current.Spec = organization.Spec
	return s.farosclientset.TenancyV1alpha1().Organizations().Update(ctx, current, metav1.UpdateOptions{})

}

// getOrganizationID returns a unique ID for a user derived from workspace user-facing name
func getOrganizationID(organization *tenancyv1alpha1.Organization) string {
	hash := sha256.Sum224([]byte(organization.Name))
	base36hash := strings.ToLower(base36.EncodeBytes(hash[:]))
	return base36hash
}
