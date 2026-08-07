// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func S3ProfileValidate(ctx context.Context, apiReader client.Reader,
	objectStoreGetter ObjectStoreGetter, s3ProfileName, listKeyPrefix string,
	log logr.Logger,
) (string, error) {
	objectStore, _, err := objectStoreGetter.ObjectStore(
		ctx, apiReader, s3ProfileName, "drpolicy validation", log)
	if err != nil {
		return "s3ConnectionFailed", fmt.Errorf("%s: %w", s3ProfileName, err)
	}

	if _, err := objectStore.ListKeys(listKeyPrefix); err != nil {
		return "s3ListFailed", fmt.Errorf("%s: %w", s3ProfileName, err)
	}

	return "", nil
}
