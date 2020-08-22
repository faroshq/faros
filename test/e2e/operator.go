package e2e

// Copyright (c) Faros.sh.
// Licensed under the Apache License 2.0.

import (
	"context"
	"fmt"
	"regexp"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"

	farosv1alpha1 "github.com/faroshq/faros/pkg/operator/apis/operator.faros.sh/v1alpha1"
)

func updatedObjects() ([]string, error) {
	pods, err := clients.Kubernetes.CoreV1().Pods("faros-operator").List(context.TODO(), metav1.ListOptions{
		LabelSelector: "app=faros-operator",
	})
	if err != nil {
		return nil, err
	}
	if len(pods.Items) != 1 {
		return nil, fmt.Errorf("%d faros-operator pods found", len(pods.Items))
	}
	b, err := clients.Kubernetes.CoreV1().Pods("faros-operator").GetLogs(pods.Items[0].Name, &corev1.PodLogOptions{}).DoRaw(context.TODO())
	if err != nil {
		return nil, err
	}

	var result []string
	rx := regexp.MustCompile(`.*msg="(Update|Create) ([a-zA-Z\/.]+).*`)
	changes := rx.FindAllStringSubmatch(string(b), -1)
	if len(changes) > 0 {
		for _, change := range changes {
			if len(change) == 3 {
				result = append(result, change[1]+" "+change[2])
			}
		}
	} else {
		log.Sugar().Infof("FindAllStringSubmatch: returned %v", changes)
	}
	return result, nil
}

var _ = Describe("FAROS Operator - Internet checking", func() {
	var originalURLs []string
	BeforeEach(func() {
		// save the originalURLs
		co, err := clients.FarosClient.Clusters().Get(context.TODO(), "cluster", metav1.GetOptions{})
		if errors.IsNotFound(err) {
			Skip("skipping tests as faros-operator is not deployed")
		}

		Expect(err).NotTo(HaveOccurred())
		originalURLs = co.Spec.InternetChecker.URLs
	})
	AfterEach(func() {
		// set the URLs back again
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			co, err := clients.FarosClient.Clusters().Get(context.TODO(), "cluster", metav1.GetOptions{})
			if err != nil {
				return err
			}
			co.Spec.InternetChecker.URLs = originalURLs
			_, err = clients.FarosClient.Clusters().Update(context.TODO(), co, metav1.UpdateOptions{})
			return err
		})
		Expect(err).NotTo(HaveOccurred())
	})
	Specify("the InternetReachable default list should all be reachable", func() {
		co, err := clients.FarosClient.Clusters().Get(context.TODO(), "cluster", metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(co.Status.Conditions.IsTrueFor(farosv1alpha1.InternetReachable)).To(BeTrue())
	})
	Specify("custom invalid site shows not InternetReachable", func() {
		// set an unreachable URL
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			co, err := clients.FarosClient.Clusters().Get(context.TODO(), "cluster", metav1.GetOptions{})
			if err != nil {
				return err
			}
			co.Spec.InternetChecker.URLs = []string{"https://localhost:1234/shouldnotexist"}
			_, err = clients.FarosClient.Clusters().Update(context.TODO(), co, metav1.UpdateOptions{})
			return err
		})
		Expect(err).NotTo(HaveOccurred())

		// confirm the conditions are correct
		err = wait.PollImmediate(10*time.Second, time.Minute, func() (bool, error) {
			co, err := clients.FarosClient.Clusters().Get(context.TODO(), "cluster", metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			log.Sugar().Info(co.Status.Conditions)
			return co.Status.Conditions.IsFalseFor(farosv1alpha1.InternetReachable), nil
		})
		Expect(err).NotTo(HaveOccurred())
	})
})
