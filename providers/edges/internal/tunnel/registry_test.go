/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package tunnel

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	ktesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/clientcmd"
)

func testRegistry(replicaID, addr string, cs *kubefake.Clientset, now func() time.Time) *Registry {
	return &Registry{
		leases:    cs.CoordinationV1().Leases(registryNamespace),
		replicaID: replicaID,
		selfAddr:  addr,
		now:       now,
		cache:     map[string]registryCacheEntry{},
	}
}

func TestRegistryClaimLookupAndTakeover(t *testing.T) {
	ctx := context.Background()
	cs := kubefake.NewClientset()
	current := time.Now()
	clock := func() time.Time { return current }
	a := testRegistry("replica-a", "10.0.0.1:8090", cs, clock)
	b := testRegistry("replica-b", "10.0.0.2:8090", cs, clock)

	const key = "kubernetesclusters/cl-1/edge-1"
	if err := a.ClaimTunnel(ctx, key); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if addr, ok := b.LookupTunnel(ctx, key); !ok || addr != "10.0.0.1:8090" {
		t.Fatalf("lookup from peer = %q/%v, want owner address", addr, ok)
	}

	// The agent reconnects to B: an unconditional re-claim overwrites A —
	// the live socket is ground truth.
	if err := b.ClaimTunnel(ctx, key); err != nil {
		t.Fatalf("re-claim: %v", err)
	}
	a.invalidate(key)
	if addr, ok := a.LookupTunnel(ctx, key); !ok || addr != "10.0.0.2:8090" {
		t.Fatalf("lookup after reconnect = %q/%v, want new owner", addr, ok)
	}

	// A's late disconnect cleanup must NOT erase B's newer claim.
	a.ReleaseTunnel(ctx, key)
	b.invalidate(key)
	if addr, ok := b.LookupTunnel(ctx, key); !ok || addr != "10.0.0.2:8090" {
		t.Fatalf("lookup after stale release = %q/%v, want claim intact", addr, ok)
	}

	// The owner's release drops it.
	b.ReleaseTunnel(ctx, key)
	b.invalidate(key)
	if _, ok := b.LookupTunnel(ctx, key); ok {
		t.Fatal("released tunnel still resolves")
	}
}

func TestRegistryExpiredLeasesDoNotResolve(t *testing.T) {
	ctx := context.Background()
	cs := kubefake.NewClientset()
	current := time.Now()
	clock := func() time.Time { return current }
	a := testRegistry("replica-a", "10.0.0.1:8090", cs, clock)
	b := testRegistry("replica-b", "10.0.0.2:8090", cs, clock)

	const key = "kubernetesclusters/cl-1/edge-1"
	if err := a.ClaimTunnel(ctx, key); err != nil {
		t.Fatalf("claim: %v", err)
	}
	current = current.Add(registryLeaseTTL + time.Second)
	if _, ok := b.LookupTunnel(ctx, key); ok {
		t.Fatal("expired tunnel lease still resolves")
	}
	if tunnels := b.ListTunnels(ctx); len(tunnels) != 0 {
		t.Fatalf("expired lease listed: %v", tunnels)
	}

	// Renewal by the owner brings it back.
	a.RenewOwned(ctx, []string{key})
	b.invalidate(key)
	if _, ok := b.LookupTunnel(ctx, key); !ok {
		t.Fatal("renewed tunnel lease does not resolve")
	}
}

func TestRegistryRenewRepairsClaimAfterTransientCreateFailure(t *testing.T) {
	ctx := context.Background()
	cs := kubefake.NewClientset()
	current := time.Now()
	clock := func() time.Time { return current }
	a := testRegistry("replica-a", "10.0.0.1:8090", cs, clock)
	b := testRegistry("replica-b", "10.0.0.2:8090", cs, clock)
	const key = "f4-repair/kubernetesclusters/cl-1/edge-1"

	failed := false
	cs.PrependReactor("create", "leases", func(action ktesting.Action) (bool, runtime.Object, error) {
		create := action.(ktesting.CreateAction)
		lease := create.GetObject().(*coordinationv1.Lease)
		if lease.Name == tunnelLeaseName(key) && !failed {
			failed = true
			return true, nil, errors.New("f4 injected tunnel claim failure")
		}
		return false, nil, nil
	})

	if err := a.ClaimTunnel(ctx, key); err == nil {
		t.Fatal("ClaimTunnel unexpectedly succeeded through the injected failure")
	}
	if !failed {
		t.Fatal("test reactor did not inject the tunnel claim failure")
	}
	a.RenewOwned(ctx, []string{key})
	b.invalidate(key)
	if addr, ok := b.LookupTunnel(ctx, key); !ok || addr != a.selfAddr {
		t.Fatalf("renewal did not repair missing claim: got %q/%v, want %q/true", addr, ok, a.selfAddr)
	}
}

func TestRegistryRenewDoesNotTakeOverForeignClaim(t *testing.T) {
	ctx := context.Background()
	cs := kubefake.NewClientset()
	a := testRegistry("replica-a", "10.0.0.1:8090", cs, time.Now)
	b := testRegistry("replica-b", "10.0.0.2:8090", cs, time.Now)
	const key = "f4-foreign/kubernetesclusters/cl-1/edge-1"

	failed := false
	cs.PrependReactor("create", "leases", func(action ktesting.Action) (bool, runtime.Object, error) {
		create := action.(ktesting.CreateAction)
		lease := create.GetObject().(*coordinationv1.Lease)
		if lease.Name == tunnelLeaseName(key) && !failed {
			failed = true
			return true, nil, errors.New("f4 injected tunnel claim failure")
		}
		return false, nil, nil
	})

	if err := a.ClaimTunnel(ctx, key); err == nil {
		t.Fatal("ClaimTunnel unexpectedly succeeded through the injected failure")
	}
	if err := b.ClaimTunnel(ctx, key); err != nil {
		t.Fatalf("foreign owner claim: %v", err)
	}
	a.RenewOwned(ctx, []string{key})
	b.invalidate(key)
	if addr, ok := b.LookupTunnel(ctx, key); !ok || addr != b.selfAddr {
		t.Fatalf("renewal overwrote foreign claim: got %q/%v, want %q/true", addr, ok, b.selfAddr)
	}
}

func TestRegistryEndedRegistrationCannotRepairForeignClaim(t *testing.T) {
	ctx := context.Background()
	cs := kubefake.NewClientset()
	a := testRegistry("replica-a", "10.0.0.1:8090", cs, time.Now)
	b := testRegistry("replica-b", "10.0.0.2:8090", cs, time.Now)
	const key = "f4-generation/kubernetesclusters/cl-1/edge-1"

	registration := a.beginRegistration(key)
	if !a.endRegistration(registration) {
		t.Fatal("failed to end the local registration")
	}
	if err := b.ClaimTunnel(ctx, key); err != nil {
		t.Fatalf("foreign owner claim: %v", err)
	}
	a.renewOwned(ctx, []tunnelRegistration{registration})
	b.invalidate(key)
	if addr, ok := b.LookupTunnel(ctx, key); !ok || addr != b.selfAddr {
		t.Fatalf("ended registration was resurrected over foreign claim: got %q/%v, want %q/true", addr, ok, b.selfAddr)
	}
}

func TestRegistryStaleRegistrationCannotReleaseReplacement(t *testing.T) {
	ctx := context.Background()
	cs := kubefake.NewClientset()
	a := testRegistry("replica-a", "10.0.0.1:8090", cs, time.Now)
	b := testRegistry("replica-b", "10.0.0.2:8090", cs, time.Now)
	const key = "f4-generation/kubernetesclusters/cl-1/edge-1"

	old := a.beginRegistration(key)
	if err := a.claimTunnel(ctx, old); err != nil {
		t.Fatalf("old claim: %v", err)
	}
	if !a.endRegistration(old) {
		t.Fatal("failed to end old registration")
	}
	fresh := a.beginRegistration(key)
	if err := a.claimTunnel(ctx, fresh); err != nil {
		t.Fatalf("fresh claim: %v", err)
	}
	if err := a.claimTunnel(ctx, old); err != nil {
		t.Fatalf("stale claim: %v", err)
	}
	lease, err := a.leases.Get(ctx, tunnelLeaseName(key), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get replacement claim: %v", err)
	}
	if got := lease.Annotations[tunnelLeaseGenerationAnno]; got != fmt.Sprint(fresh.generation) {
		t.Fatalf("stale claim changed generation marker to %q, want %d", got, fresh.generation)
	}
	a.releaseTunnel(ctx, old)
	b.invalidate(key)
	if addr, ok := b.LookupTunnel(ctx, key); !ok || addr != a.selfAddr {
		t.Fatalf("stale release removed replacement: got %q/%v, want %q/true", addr, ok, a.selfAddr)
	}

	if !a.endRegistration(fresh) {
		t.Fatal("failed to end fresh registration")
	}
	a.releaseTunnel(ctx, fresh)
	b.invalidate(key)
	if _, ok := b.LookupTunnel(ctx, key); ok {
		t.Fatal("fresh release did not remove replacement claim")
	}
}

func TestRegistryStaleInitialClaimCannotOverwriteNewerGenerationOnCreateRetry(t *testing.T) {
	ctx := context.Background()
	cs := kubefake.NewClientset()
	a := testRegistry("replica-a", "10.0.0.1:8090", cs, time.Now)
	const key = "f4-generation-create/kubernetesclusters/cl-1/edge-1"

	old := a.beginRegistration(key)
	var fresh tunnelRegistration
	var nestedErr error
	firstCreate := true
	cs.PrependReactor("create", "leases", func(action ktesting.Action) (bool, runtime.Object, error) {
		create := action.(ktesting.CreateAction)
		lease := create.GetObject().(*coordinationv1.Lease)
		if lease.Name != tunnelLeaseName(key) || !firstCreate {
			return false, nil, nil
		}
		firstCreate = false
		fresh = a.beginRegistration(key)
		// Make the replacement claim visible before the stale first Create
		// returns AlreadyExists. The stale retry must check its generation
		// before the next GET/Create attempt and leave this claim untouched.
		replacement := lease.DeepCopy()
		if replacement.Annotations == nil {
			replacement.Annotations = map[string]string{}
		}
		replacement.Annotations[tunnelLeaseGenerationAnno] = fmt.Sprint(fresh.generation)
		nestedErr = cs.Tracker().Create(
			coordinationv1.SchemeGroupVersion.WithResource("leases"),
			replacement,
			registryNamespace,
		)
		return true, nil, apierrors.NewAlreadyExists(schema.GroupResource{Resource: "leases"}, lease.Name)
	})

	if err := a.claimTunnel(ctx, old); err != nil {
		t.Fatalf("stale initial claim returned an error: %v", err)
	}
	if nestedErr != nil {
		t.Fatalf("install newer generation during forced create race: %v", nestedErr)
	}
	lease, err := a.leases.Get(ctx, tunnelLeaseName(key), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get newer claim: %v", err)
	}
	if got := lease.Annotations[tunnelLeaseGenerationAnno]; got != fmt.Sprint(fresh.generation) {
		t.Fatalf("stale create retry changed generation marker to %q, want %d", got, fresh.generation)
	}

	if !a.endRegistration(fresh) {
		t.Fatal("failed to end fresh registration")
	}
	a.releaseTunnel(ctx, fresh)
}

func TestRegistryStaleClaimCannotOverwriteNewerGenerationOnRetry(t *testing.T) {
	ctx := context.Background()
	cs := kubefake.NewClientset()
	a := testRegistry("replica-a", "10.0.0.1:8090", cs, time.Now)
	const key = "f4-generation-retry/kubernetesclusters/cl-1/edge-1"

	// Seed an existing self-held Lease so the stale claim takes the Update path.
	if err := a.ClaimTunnel(ctx, key); err != nil {
		t.Fatalf("seed claim: %v", err)
	}
	old := a.beginRegistration(key)
	var fresh tunnelRegistration
	var nestedErr error
	firstUpdate := true
	cs.PrependReactor("update", "leases", func(action ktesting.Action) (bool, runtime.Object, error) {
		update := action.(ktesting.UpdateAction)
		lease := update.GetObject().(*coordinationv1.Lease)
		if lease.Name != tunnelLeaseName(key) || !firstUpdate {
			return false, nil, nil
		}
		firstUpdate = false
		fresh = a.beginRegistration(key)
		// Install the newer claim before the stale operation receives its forced
		// conflict. The retry therefore GETs a real, newer generation, which is
		// the interleaving that used to let the stale operation overwrite the
		// replacement and then delete it during cleanup. Updating the tracker
		// directly avoids recursively invoking a fake client reactor while it
		// holds the client's action lock.
		replacement := lease.DeepCopy()
		if replacement.Annotations == nil {
			replacement.Annotations = map[string]string{}
		}
		replacement.Annotations[tunnelLeaseGenerationAnno] = fmt.Sprint(fresh.generation)
		nestedErr = cs.Tracker().Update(
			coordinationv1.SchemeGroupVersion.WithResource("leases"),
			replacement,
			registryNamespace,
		)
		return true, nil, apierrors.NewConflict(schema.GroupResource{Resource: "leases"}, lease.Name, errors.New("f4 forced claim retry"))
	})

	if err := a.claimTunnel(ctx, old); err != nil {
		t.Fatalf("stale claim returned an error: %v", err)
	}
	if nestedErr != nil {
		t.Fatalf("install newer generation during forced retry: %v", nestedErr)
	}
	lease, err := a.leases.Get(ctx, tunnelLeaseName(key), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get newer claim: %v", err)
	}
	if got := lease.Annotations[tunnelLeaseGenerationAnno]; got != fmt.Sprint(fresh.generation) {
		t.Fatalf("stale retry changed generation marker to %q, want %d", got, fresh.generation)
	}

	if !a.endRegistration(fresh) {
		t.Fatal("failed to end fresh registration")
	}
	a.releaseTunnel(ctx, fresh)
}

func TestRegistryListAndPresence(t *testing.T) {
	ctx := context.Background()
	cs := kubefake.NewClientset()
	clock := time.Now
	a := testRegistry("replica-a", "10.0.0.1:8090", cs, clock)
	b := testRegistry("replica-b", "10.0.0.2:8090", cs, clock)

	_ = a.ClaimTunnel(ctx, "kubernetesclusters/cl-1/edge-1")
	_ = b.ClaimTunnel(ctx, "linuxservers/cl-2/edge-2")
	a.RenewOwned(ctx, nil) // writes presence
	b.RenewOwned(ctx, nil)

	tunnels := a.ListTunnels(ctx)
	if len(tunnels) != 2 ||
		tunnels["kubernetesclusters/cl-1/edge-1"] != "10.0.0.1:8090" ||
		tunnels["linuxservers/cl-2/edge-2"] != "10.0.0.2:8090" {
		t.Fatalf("ListTunnels = %v, want both claims with owner addresses", tunnels)
	}

	if addr, ok := a.ReplicaAddr(ctx, "replica-b"); !ok || addr != "10.0.0.2:8090" {
		t.Fatalf("ReplicaAddr(replica-b) = %q/%v, want peer address", addr, ok)
	}
	if addr, ok := a.ReplicaAddr(ctx, "replica-a"); !ok || addr != "10.0.0.1:8090" {
		t.Fatalf("ReplicaAddr(self) = %q/%v, want self address without a lease read", addr, ok)
	}
	if _, ok := a.ReplicaAddr(ctx, "replica-zombie"); ok {
		t.Fatal("unknown replica resolved")
	}
}

func TestSanitizeReplicaID(t *testing.T) {
	cases := map[string]bool{ // input → expect same string back
		"edges-5b8f7c9d4-x2vlp": true,
		"MacBook.local":         false,
		"":                      false,
	}
	for in, wantSame := range cases {
		got := SanitizeReplicaID(in)
		if got == "" {
			t.Fatalf("SanitizeReplicaID(%q) produced empty id", in)
		}
		if wantSame && got != in {
			t.Fatalf("SanitizeReplicaID(%q) = %q, want unchanged", in, got)
		}
		for _, c := range got {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				t.Fatalf("SanitizeReplicaID(%q) = %q contains %q", in, got, string(c))
			}
		}
	}
}

type failFirstTunnelCreateFault struct {
	mu     sync.Mutex
	failed bool
}

type failFirstTunnelCreateTransport struct {
	base  http.RoundTripper
	fault *failFirstTunnelCreateFault
}

func (t *failFirstTunnelCreateTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/leases") && req.Body != nil {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
		if bytes.Contains(body, []byte(tunnelLeasePrefix)) {
			t.fault.mu.Lock()
			if !t.fault.failed {
				t.fault.failed = true
				t.fault.mu.Unlock()
				return nil, errors.New("f4 injected initial tunnel lease transport failure")
			}
			t.fault.mu.Unlock()
		}
	}
	return t.base.RoundTrip(req)
}

func TestRegistryReplicaConvergenceLive(t *testing.T) {
	kubeconfig := strings.TrimSpace(os.Getenv("EDGES_REPLICA_TEST_KUBECONFIG"))
	if kubeconfig == "" {
		t.Skip("EDGES_REPLICA_TEST_KUBECONFIG is not set")
	}

	baseConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		t.Fatalf("build live Kubernetes config: %v", err)
	}
	configA := rest.CopyConfig(baseConfig)
	configB := rest.CopyConfig(baseConfig)
	fault := &failFirstTunnelCreateFault{}
	wrap := func(base http.RoundTripper) http.RoundTripper {
		return &failFirstTunnelCreateTransport{base: base, fault: fault}
	}
	configA.WrapTransport = wrap
	configB.WrapTransport = wrap

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	key := "f4-live-" + suffix + "/kubernetesclusters/cluster/edge"
	replicaIDa := SanitizeReplicaID("f4-live-a-" + suffix)
	replicaIDb := SanitizeReplicaID("f4-live-b-" + suffix)
	addrA := "f4-live-a-" + suffix + ":8090"
	addrB := "f4-live-b-" + suffix + ":8090"

	a, err := NewRegistry(configA, replicaIDa, addrA)
	if err != nil {
		t.Fatalf("create registry A: %v", err)
	}
	b, err := NewRegistry(configB, replicaIDb, addrB)
	if err != nil {
		t.Fatalf("create registry B: %v", err)
	}
	leaseNames := []string{
		tunnelLeaseName(key),
		presenceLeasePrefix + replicaIDa,
		presenceLeasePrefix + replicaIDb,
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, name := range leaseNames {
			if err := a.leases.Delete(cleanupCtx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
				t.Logf("cleanup lease %q: %v", name, err)
			}
		}
	})

	registration := a.beginRegistration(key)
	claimCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.claimTunnel(claimCtx, registration); err == nil {
		t.Fatal("initial claim unexpectedly succeeded through injected transport failure")
	}
	a.renewOwned(claimCtx, []tunnelRegistration{registration})

	if err := wait.PollUntilContextTimeout(context.Background(), 100*time.Millisecond, 10*time.Second, true,
		func(ctx context.Context) (bool, error) {
			b.invalidate(key)
			addr, ok := b.LookupTunnel(ctx, key)
			return ok && addr == addrA, nil
		}); err != nil {
		t.Fatalf("peer did not resolve repaired claim: %v", err)
	}

	if err := b.ClaimTunnel(claimCtx, key); err != nil {
		t.Fatalf("newer owner claim: %v", err)
	}
	a.renewOwned(claimCtx, []tunnelRegistration{registration})
	b.invalidate(key)
	if addr, ok := b.LookupTunnel(claimCtx, key); !ok || addr != addrB {
		t.Fatalf("old renewal replaced newer owner: got %q/%v, want %q/true", addr, ok, addrB)
	}
	if !a.endRegistration(registration) {
		t.Fatal("failed to end live-test registration")
	}
}
