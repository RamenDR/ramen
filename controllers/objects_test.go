// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	"context"
	"time"

	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/format"
	gomegatypes "github.com/onsi/gomega/types"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func objectGet(namespacedName types.NamespacedName, object client.Object) error {
	return apiReader.Get(context.TODO(), namespacedName, object)
}

func objectDeletionTimestampRecentVerify(namespacedName types.NamespacedName, object client.Object) {
	Eventually(func() *metav1.Time {
		Expect(objectGet(namespacedName, object)).To(Succeed())

		return object.GetDeletionTimestamp()
	}).ShouldNot(BeNil())
	Expect(object.GetDeletionTimestamp().Time).Should(BeTemporally("~", time.Now(), 2*time.Second),
		format.Object(object, 0))
}

func objectNotFoundErrorMatch(groupResource schema.GroupResource, objectName string) gomegatypes.GomegaMatcher {
	return MatchError(errors.NewNotFound(groupResource, objectName))
}

func objectAbsentVerify(namespacedName types.NamespacedName, object client.Object, groupResource schema.GroupResource) {
	var err error

	Eventually(func() error {
		err = objectGet(namespacedName, object)

		return err
	}).ShouldNot(BeNil())
	Expect(err).To(objectNotFoundErrorMatch(groupResource, namespacedName.Name), format.Object(object, 0))
}

func objectOrItsFinalizerAbsentVerify(namespacedName types.NamespacedName, object client.Object,
	groupResource schema.GroupResource, finalizerName string,
) (objectAbsent bool) {
	Eventually(func() []string {
		if err := objectGet(namespacedName, object); err != nil {
			Expect(err).To(objectNotFoundErrorMatch(groupResource, namespacedName.Name), format.Object(object, 0))
			objectAbsent = true

			return nil
		}

		return object.GetFinalizers()
	}).ShouldNot(ContainElement(finalizerName))

	return
}

func conditionExpect(conditions []metav1.Condition, typ string) *metav1.Condition {
	condition := meta.FindStatusCondition(conditions, typ)
	Expect(condition).ToNot(BeNil())

	return condition
}
