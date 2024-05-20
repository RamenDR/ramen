// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package volsync

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/go-logr/logr"
	rmnutil "github.com/ramendr/ramen/internal/controller/util"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const tlsPSKDataSize = 64

// Creates a new volsync replication secret on the cluster (should be called on the hub cluster).  If the secret
// already exists, nop
func ReconcileVolSyncReplicationSecret(ctx context.Context, k8sClient client.Client, ownerObject metav1.Object,
	secretName, secretNamespace string, log logr.Logger) (*corev1.Secret, error,
) {
	existingSecret := &corev1.Secret{}
	// See if it exists already
	err := k8sClient.Get(ctx, types.NamespacedName{Name: secretName, Namespace: secretNamespace}, existingSecret)
	if err != nil && !kerrors.IsNotFound(err) {
		log.Error(err, "failed to get secret")

		return nil, fmt.Errorf("failed to get secret (%w)", err)
	}

	if err == nil { // Found the secret, going to assume it doesn't need modification
		return existingSecret, nil
	}

	secret, err := generateNewVolSyncReplicationSecret(secretName, secretNamespace, log)
	if err != nil {
		return nil, err
	}

	if err := ctrl.SetControllerReference(ownerObject, secret, k8sClient.Scheme()); err != nil {
		log.Error(err, "unable to set controller reference on secret")

		return nil, fmt.Errorf("%w", err)
	}

	log.Info("Creating new volsync rsync secret", "secretName", secretName)

	err = k8sClient.Create(ctx, secret)
	if err != nil {
		log.Error(err, "Error creating secret", "secretName", secretName)

		return nil, fmt.Errorf("error creating secret for volsync (%w)", err)
	}

	return secret, nil
}

// Pre-shared key for rsync TLS mover
func generateNewVolSyncReplicationSecret(secretName, secretNamespace string, log logr.Logger) (*corev1.Secret, error,
) {
	tlsKey, err := genTLSPreSharedKey(log)
	if err != nil {
		log.Error(err, "Unable to generate new tls secret for VolSync replication")

		return nil, err
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: secretNamespace,
			Labels: map[string]string{
				rmnutil.OCMBackupLabelKey: rmnutil.OCMBackupLabelValue,
			},
		},
		StringData: map[string]string{
			"psk.txt": "volsyncramen:" + tlsKey,
		},
	}

	return secret, nil
}

func genTLSPreSharedKey(log logr.Logger) (string, error) {
	pskData := make([]byte, tlsPSKDataSize)
	if _, err := rand.Read(pskData); err != nil {
		log.Error(err, "error generating tls key")

		return "", err
	}

	return hex.EncodeToString(pskData), nil
}
