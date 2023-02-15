module github.com/faroshq/faros

go 1.19

replace (
	k8s.io/api => github.com/kcp-dev/kubernetes/staging/src/k8s.io/api v0.0.0-20230210192259-aaa28aa88b2d
	k8s.io/apiextensions-apiserver => github.com/kcp-dev/kubernetes/staging/src/k8s.io/apiextensions-apiserver v0.0.0-20230210192259-aaa28aa88b2d
	k8s.io/apimachinery => github.com/kcp-dev/kubernetes/staging/src/k8s.io/apimachinery v0.0.0-20230210192259-aaa28aa88b2d
	k8s.io/apiserver => github.com/kcp-dev/kubernetes/staging/src/k8s.io/apiserver v0.0.0-20230210192259-aaa28aa88b2d
	k8s.io/cli-runtime => github.com/kcp-dev/kubernetes/staging/src/k8s.io/cli-runtime v0.0.0-20230210192259-aaa28aa88b2d
	k8s.io/client-go => github.com/kcp-dev/kubernetes/staging/src/k8s.io/client-go v0.0.0-20230210192259-aaa28aa88b2d
	k8s.io/cloud-provider => github.com/kcp-dev/kubernetes/staging/src/k8s.io/cloud-provider v0.0.0-20230210192259-aaa28aa88b2d
	k8s.io/cluster-bootstrap => github.com/kcp-dev/kubernetes/staging/src/k8s.io/cluster-bootstrap v0.0.0-20230210192259-aaa28aa88b2d
	k8s.io/code-generator => github.com/kcp-dev/kubernetes/staging/src/k8s.io/code-generator v0.0.0-20230210192259-aaa28aa88b2d
	k8s.io/component-base => github.com/kcp-dev/kubernetes/staging/src/k8s.io/component-base v0.0.0-20230210192259-aaa28aa88b2d
	k8s.io/component-helpers => github.com/kcp-dev/kubernetes/staging/src/k8s.io/component-helpers v0.0.0-20230210192259-aaa28aa88b2d
	k8s.io/controller-manager => github.com/kcp-dev/kubernetes/staging/src/k8s.io/controller-manager v0.0.0-20230210192259-aaa28aa88b2d
	k8s.io/cri-api => github.com/kcp-dev/kubernetes/staging/src/k8s.io/cri-api v0.0.0-20230210192259-aaa28aa88b2d
	k8s.io/csi-translation-lib => github.com/kcp-dev/kubernetes/staging/src/k8s.io/csi-translation-lib v0.0.0-20230210192259-aaa28aa88b2d
	k8s.io/kube-aggregator => github.com/kcp-dev/kubernetes/staging/src/k8s.io/kube-aggregator v0.0.0-20230210192259-aaa28aa88b2d
	k8s.io/kube-controller-manager => github.com/kcp-dev/kubernetes/staging/src/k8s.io/kube-controller-manager v0.0.0-20230210192259-aaa28aa88b2d
	k8s.io/kube-proxy => github.com/kcp-dev/kubernetes/staging/src/k8s.io/kube-proxy v0.0.0-20230210192259-aaa28aa88b2d
	k8s.io/kube-scheduler => github.com/kcp-dev/kubernetes/staging/src/k8s.io/kube-scheduler v0.0.0-20230210192259-aaa28aa88b2d
	k8s.io/kubectl => github.com/kcp-dev/kubernetes/staging/src/k8s.io/kubectl v0.0.0-20230210192259-aaa28aa88b2d
	k8s.io/kubelet => github.com/kcp-dev/kubernetes/staging/src/k8s.io/kubelet v0.0.0-20230210192259-aaa28aa88b2d
	k8s.io/kubernetes => github.com/kcp-dev/kubernetes v0.0.0-20230210192259-aaa28aa88b2d
	k8s.io/legacy-cloud-providers => github.com/kcp-dev/kubernetes/staging/src/k8s.io/legacy-cloud-providers v0.0.0-20230210192259-aaa28aa88b2d
	k8s.io/metrics => github.com/kcp-dev/kubernetes/staging/src/k8s.io/metrics v0.0.0-20230210192259-aaa28aa88b2d
	k8s.io/mount-utils => github.com/kcp-dev/kubernetes/staging/src/k8s.io/mount-utils v0.0.0-20230210192259-aaa28aa88b2d
	k8s.io/pod-security-admission => github.com/kcp-dev/kubernetes/staging/src/k8s.io/pod-security-admission v0.0.0-20230210192259-aaa28aa88b2d
	k8s.io/sample-apiserver => github.com/kcp-dev/kubernetes/staging/src/k8s.io/sample-apiserver v0.0.0-20230210192259-aaa28aa88b2d
)

require (
	github.com/InVisionApp/go-health/v2 v2.1.3
	github.com/aojea/h2rev2 v0.0.0-20221113112042-95e0258785ad
	github.com/coreos/go-oidc v2.2.1+incompatible
	github.com/go-bindata/go-bindata/v3 v3.1.3
	github.com/golang-jwt/jwt v3.2.2+incompatible
	github.com/google/uuid v1.3.0
	github.com/gorilla/handlers v1.5.1
	github.com/gorilla/mux v1.8.0
	github.com/gorilla/sessions v1.2.1
	github.com/jackc/pgx/v4 v4.18.0
	github.com/joho/godotenv v1.5.1
	github.com/kcp-dev/apimachinery v0.0.0-20221102195355-d65878bc16be
	github.com/kcp-dev/apimachinery/v2 v2.0.0-alpha.0
	github.com/kcp-dev/client-go v0.0.0-20221215092857-c1e5154a9825
	github.com/kcp-dev/kcp v0.10.0
	github.com/kcp-dev/kcp/pkg/apis v0.10.0
	github.com/kcp-dev/logicalcluster/v3 v3.0.2
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/pkg/errors v0.9.1
	github.com/skratchdot/open-golang v0.0.0-20200116055534-eef842397966
	github.com/spf13/cobra v1.6.1
	github.com/spf13/pflag v1.0.6-0.20210604193023-d5e0c0615ace
	github.com/stretchr/testify v1.8.1
	go.uber.org/zap v1.24.0
	golang.org/x/net v0.6.0
	golang.org/x/oauth2 v0.0.0-20220822191816-0ebed06d0094
	golang.org/x/tools v0.4.0
	gorm.io/driver/postgres v1.4.7
	gorm.io/driver/sqlite v1.4.4
	gorm.io/gorm v1.24.5
	k8s.io/apiextensions-apiserver v0.26.1
	k8s.io/apimachinery v0.26.1
	k8s.io/cli-runtime v0.24.3
	k8s.io/client-go v0.26.1
	k8s.io/code-generator v0.26.1
	k8s.io/klog v1.0.0
	k8s.io/klog/v2 v2.80.1
	k8s.io/utils v0.0.0-20221128185143-99ec85e7a448
	sigs.k8s.io/controller-tools v0.11.3
	sigs.k8s.io/yaml v1.3.0
)

require (
	github.com/InVisionApp/go-logger v1.0.1 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/emicklei/go-restful/v3 v3.9.0 // indirect
	github.com/evanphx/json-patch v5.6.0+incompatible // indirect
	github.com/fatih/color v1.13.0 // indirect
	github.com/felixge/httpsnoop v1.0.1 // indirect
	github.com/go-errors/errors v1.0.1 // indirect
	github.com/go-logr/logr v1.2.3 // indirect
	github.com/go-openapi/jsonpointer v0.19.5 // indirect
	github.com/go-openapi/jsonreference v0.20.0 // indirect
	github.com/go-openapi/swag v0.19.14 // indirect
	github.com/gobuffalo/flect v0.3.0 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/protobuf v1.5.2 // indirect
	github.com/google/btree v1.0.1 // indirect
	github.com/google/gnostic v0.5.7-v3refs // indirect
	github.com/google/go-cmp v0.5.9 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/google/shlex v0.0.0-20191202100458-e7afc7fbc510 // indirect
	github.com/gorilla/securecookie v1.1.1 // indirect
	github.com/gregjones/httpcache v0.0.0-20180305231024-9cad4c3443a7 // indirect
	github.com/imdario/mergo v0.3.12 // indirect
	github.com/inconshreveable/mousetrap v1.0.1 // indirect
	github.com/jackc/chunkreader/v2 v2.0.1 // indirect
	github.com/jackc/pgconn v1.14.0 // indirect
	github.com/jackc/pgio v1.0.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgproto3/v2 v2.3.2 // indirect
	github.com/jackc/pgservicefile v0.0.0-20221227161230-091c0ba34f0a // indirect
	github.com/jackc/pgtype v1.14.0 // indirect
	github.com/jackc/pgx/v5 v5.2.0 // indirect
	github.com/jackc/puddle v1.3.0 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/kcp-dev/logicalcluster/v2 v2.0.0-alpha.4 // indirect
	github.com/kisielk/errcheck v1.5.0 // indirect
	github.com/liggitt/tabwriter v0.0.0-20181228230101-89fcab3d43de // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/mattn/go-colorable v0.1.9 // indirect
	github.com/mattn/go-isatty v0.0.14 // indirect
	github.com/mattn/go-sqlite3 v1.14.15 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/monochromegane/go-gitignore v0.0.0-20200626010858-205db1a8cc00 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/onsi/gomega v1.24.2 // indirect
	github.com/peterbourgon/diskv v2.0.1+incompatible // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/pquerna/cachecontrol v0.0.0-20171018203845-0dec1b30a021 // indirect
	github.com/xlab/treeprint v0.0.0-20181112141820-a009c3971eca // indirect
	go.starlark.net v0.0.0-20200306205701-8dd3e2ee1dd5 // indirect
	go.uber.org/atomic v1.10.0 // indirect
	go.uber.org/goleak v1.2.0 // indirect
	go.uber.org/multierr v1.7.0 // indirect
	golang.org/x/crypto v0.6.0 // indirect
	golang.org/x/lint v0.0.0-20210508222113-6edffad5e616 // indirect
	golang.org/x/mod v0.7.0 // indirect
	golang.org/x/sys v0.5.0 // indirect
	golang.org/x/term v0.5.0 // indirect
	golang.org/x/text v0.7.0 // indirect
	golang.org/x/time v0.3.0 // indirect
	google.golang.org/appengine v1.6.7 // indirect
	google.golang.org/genproto v0.0.0-20220616135557-88e70c0c3a90 // indirect
	google.golang.org/protobuf v1.28.1 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/square/go-jose.v2 v2.6.0 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	k8s.io/api v0.26.1 // indirect
	k8s.io/gengo v0.0.0-20211129171323-c02415ce4185 // indirect
	k8s.io/kube-openapi v0.0.0-20221012153701-172d655c2280 // indirect
	sigs.k8s.io/json v0.0.0-20220713155537-f223a00ba0e2 // indirect
	sigs.k8s.io/kustomize/api v0.11.4 // indirect
	sigs.k8s.io/kustomize/kyaml v0.13.6 // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.2.3 // indirect
)
