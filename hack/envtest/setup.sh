
#!/bin/bash

ENV_TEST_VERSION="1.22.x!"

for i in "$@"; do
  case $i in
    -v)
      ENV_TEST_VERSION="$OPTARG"
      echo "test version: $ENV_TEST_VERSION"
      shift
      ;;
    -c)
      echo "Preparing envtest: ${ENV_TEST_VERSION}"
      source <(go run sigs.k8s.io/controller-runtime/tools/setup-envtest use -p env "${ENV_TEST_VERSION}")
      shift
      ;;
    -r)
      echo "Deleting envtest"
      go run sigs.k8s.io/controller-runtime/tools/setup-envtest cleanup
      shift
      ;;
    -*|--*)
      echo "-v <version> -c  - create specific version of envtest"
      echo "-c - create envtest"
      echo "-r - remove envtest"
      exit 1
      ;;
 esac
done
