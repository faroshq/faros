package k8s

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
)

func (s *store) GetOrganization(ctx context.Context, organization tenancyv1alpha1.Organization) (*tenancyv1alpha1.Organization, error) {
	current, err := s.farosclientset.TenancyV1alpha1().Organizations().Get(ctx, organization.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return current, nil
}

func (s *store) ListOrganizations(ctx context.Context, organization tenancyv1alpha1.Organization) (*tenancyv1alpha1.OrganizationList, error) {
	organizations, err := s.farosclientset.TenancyV1alpha1().Organizations().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return organizations, nil
}

func (s *store) DeleteOrganization(ctx context.Context, organization tenancyv1alpha1.Organization) error {
	return s.farosclientset.TenancyV1alpha1().Organizations().Delete(ctx, organization.Name, metav1.DeleteOptions{})
}

func (s *store) CreateOrganization(ctx context.Context, organization tenancyv1alpha1.Organization) (*tenancyv1alpha1.Organization, error) {
	return s.farosclientset.TenancyV1alpha1().Organizations().Create(ctx, &organization, metav1.CreateOptions{})
}

func (s *store) UpdateOrganization(ctx context.Context, organization tenancyv1alpha1.Organization) (*tenancyv1alpha1.Organization, error) {
	current, err := s.farosclientset.TenancyV1alpha1().Organizations().Get(ctx, organization.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	current.Spec = organization.Spec
	return s.farosclientset.TenancyV1alpha1().Organizations().Update(ctx, current, metav1.UpdateOptions{})

}
