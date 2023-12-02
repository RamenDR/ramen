// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

func namespaceCreateAndDeferDeleteOrConfirmAlreadyExists(name string) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	err := k8sClient.Create(context.TODO(), ns)

	if !namespaceDeletionSupported {
		Expect(err).To(Or(BeNil(), MatchError(
			errors.NewAlreadyExists(schema.GroupResource{Resource: "namespaces"}, ns.Name),
		)))

		return
	}

	Expect(err).To(BeNil())
	DeferCleanup(func() {
		Expect(k8sClient.Delete(context.TODO(), ns)).To(Succeed())
		Eventually(apiReader.Get).WithArguments(context.TODO(), types.NamespacedName{Name: ns.Name}, ns).
			Should(MatchError(errors.NewNotFound(schema.GroupResource{Resource: "namespaces"}, ns.Name)))
	})
}
