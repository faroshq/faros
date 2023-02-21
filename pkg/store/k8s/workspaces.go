package k8s

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
	"github.com/faroshq/faros/pkg/models"
)

func (s *store) GetWorkspace(ctx context.Context, workspace tenancyv1alpha1.Workspace) (*tenancyv1alpha1.Workspace, error) {
	current, err := s.farosclientset.TenancyV1alpha1().Workspaces().Get(ctx, workspace.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return current, nil
}

func (s *store) ListWorkspaces(ctx context.Context, organizationName string, workspace tenancyv1alpha1.Workspace) (*tenancyv1alpha1.WorkspaceList, error) {
	workspaces, err := s.farosclientset.TenancyV1alpha1().Workspaces().List(ctx, metav1.ListOptions{
		LabelSelector: models.LabelSelectorForOrganization(organizationName),
	})
	if err != nil {
		return nil, err
	}
	return workspaces, nil
}

func (s *store) DeleteWorkspace(ctx context.Context, workspace tenancyv1alpha1.Workspace) error {
	return s.farosclientset.TenancyV1alpha1().Workspaces().Delete(ctx, workspace.Name, metav1.DeleteOptions{})
}

func (s *store) CreateWorkspace(ctx context.Context, workspace tenancyv1alpha1.Workspace) (*tenancyv1alpha1.Workspace, error) {
	return s.farosclientset.TenancyV1alpha1().Workspaces().Create(ctx, &workspace, metav1.CreateOptions{})
}

func (s *store) UpdateWorkspace(ctx context.Context, workspace tenancyv1alpha1.Workspace) (*tenancyv1alpha1.Workspace, error) {
	current, err := s.farosclientset.TenancyV1alpha1().Workspaces().Get(ctx, workspace.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	current.Spec = workspace.Spec
	return s.farosclientset.TenancyV1alpha1().Workspaces().Update(ctx, current, metav1.UpdateOptions{})

}
