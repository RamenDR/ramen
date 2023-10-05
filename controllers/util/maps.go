// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

func MapCopy[M ~map[K]V, K, V comparable](src M, dst *M) bool {
	if *dst == nil {
		*dst = src

		return src != nil
	}

	d := *dst

	var diff bool

	for key, value := range src {
		if d[key] == value {
			continue
		}

		d[key] = value
		diff = true
	}

	return diff
}

func MapCopyF[M ~map[K]V, K, V comparable](src M, dstGet func() M, dstSet func(M)) bool {
	dst := dstGet()
	diff := MapCopy(src, &dst)
	dstSet(dst)

	return diff
}
