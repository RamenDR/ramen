package volsync

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"

	"golang.org/x/crypto/ssh"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const keyBitSize = 4096

//TODO: update roles for whoever calls this (drplacementcontrol) - will need permissions to read/update/create secrets

// Creates a new volsync replication secret on the cluster (should be called on the hub cluster).  If the secret
// already exists, nop
func ReconcileVolSyncReplicationSecret(ctx context.Context, k8sClient client.Client, ownerObject metav1.Object,
	secretName, secretNamespace string, log logr.Logger) (*corev1.Secret, error) {

	existingSecret := &corev1.Secret{}
	// See if it exists already
	err := k8sClient.Get(ctx, types.NamespacedName{Name: secretName, Namespace: secretNamespace}, existingSecret)
	if err != nil && !kerrors.IsNotFound(err) {
		log.Error(err, "failed to get secret")
		return nil, err
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
		return nil, err
	}

	log.Info("Creating new volsync rsync secret", "secretName", secretName)
	err = k8sClient.Create(ctx, secret)
	return secret, err
}

func generateNewVolSyncReplicationSecret(secretName, secretNamespace string, log logr.Logger) (*corev1.Secret, error) {
	priv, pub, err := generateKeyPair(log)
	if err != nil {
		log.Error(err, "Unable to generate new secret for VolSync replication")
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: secretNamespace,
		},
		Data: map[string][]byte{
			"source":          priv,
			"source.pub":      pub,
			"destination":     priv,
			"destination.pub": pub,
		},
	}

	return secret, nil
}

func generateKeyPair(log logr.Logger) (priv []byte, pub []byte, err error) {
	rsaPrivateKey, err := generateNewPrivateKey(log)
	if err != nil {
		return nil, nil, err
	}

	priv = getPrivateKeyPEMBytes(rsaPrivateKey)
	pub, err = getPublicKeyBytes(rsaPrivateKey)

	return priv, pub, err
}

func generateNewPrivateKey(log logr.Logger) (*rsa.PrivateKey, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, keyBitSize)
	if err != nil {
		log.Error(err, "Unable to generate new rsa private key")
		return nil, err
	}
	if err = privateKey.Validate(); err != nil {
		log.Error(err, "Error validating new rsa private key")
		return nil, err
	}

	return privateKey, nil
}

func getPrivateKeyPEMBytes(privateKey *rsa.PrivateKey) []byte {
	privateKeyPEMBlock := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	}
	return pem.EncodeToMemory(privateKeyPEMBlock)
}

func getPublicKeyBytes(privateKey *rsa.PrivateKey) ([]byte, error) {
	pub, err := ssh.NewPublicKey(&privateKey.PublicKey)
	if err != nil {
		return nil, err
	}
	return ssh.MarshalAuthorizedKey(pub), nil
}
