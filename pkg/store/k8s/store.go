package k8s

import (
	"context"
	"crypto/sha256"
	"strings"

	"github.com/faroshq/faros/pkg/config"
	"github.com/martinlindhe/base36"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
	farosclientset "github.com/faroshq/faros/pkg/client/clientset/versioned"
	utilkubernetes "github.com/faroshq/faros/pkg/util/kubernetes"
)

type store struct {
	config         config.Config
	farosclientset *farosclientset.Clientset
}

func New(ctx context.Context, config config.Config) (*store, error) {
	cf, err := utilkubernetes.NewClientFactory(config.FarosKCPConfig.KCPClusterRestConfig)
	if err != nil {
		return nil, err
	}

	rest, err := cf.GetWorkspaceRestConfig(ctx, config.FarosKCPConfig.ControllersTenantWorkspace)
	if err != nil {
		return nil, err
	}

	client, err := farosclientset.NewForConfig(rest)
	if err != nil {
		return nil, err
	}

	return &store{
		config:         config,
		farosclientset: client,
	}, nil
}

func (s *store) GetUser(ctx context.Context, user tenancyv1alpha1.User) (*tenancyv1alpha1.User, error) {
	return s.farosclientset.TenancyV1alpha1().Users().Get(ctx, getUserID(&user), metav1.GetOptions{})
}

func (s *store) ListUsers(ctx context.Context, user tenancyv1alpha1.User) (*tenancyv1alpha1.UserList, error) {
	return s.farosclientset.TenancyV1alpha1().Users().List(ctx, metav1.ListOptions{})
}

func (s *store) DeleteUser(ctx context.Context, user tenancyv1alpha1.User) error {
	return s.farosclientset.TenancyV1alpha1().Users().Delete(ctx, getUserID(&user), metav1.DeleteOptions{})
}

func (s *store) CreateUser(ctx context.Context, user tenancyv1alpha1.User) (*tenancyv1alpha1.User, error) {
	user.Name = getUserID(&user)
	return s.farosclientset.TenancyV1alpha1().Users().Create(ctx, &user, metav1.CreateOptions{})
}

func (s *store) UpdateUser(ctx context.Context, user tenancyv1alpha1.User) (*tenancyv1alpha1.User, error) {
	current, err := s.farosclientset.TenancyV1alpha1().Users().Get(ctx, getUserID(&user), metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	current.Spec = user.Spec
	return s.farosclientset.TenancyV1alpha1().Users().Update(ctx, current, metav1.UpdateOptions{})

}

// getUserID returns a unique ID for a user derived from user email
func getUserID(user *tenancyv1alpha1.User) string {
	hash := sha256.Sum224([]byte(user.Spec.Email))
	base36hash := strings.ToLower(base36.EncodeBytes(hash[:]))
	return base36hash
}
