// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"golang.org/x/exp/maps" // TODO "maps" in go1.21+
)

// Copies src's key-value pairs into dst.
// Or, if dst is nil, assigns src to dst.
// Returns whether dst changes.
func MapCopy[M ~map[K]V, K, V comparable](src M, dst *M) bool {
	if *dst == nil {
		*dst = src

		return src != nil
	}

	d := *dst

	var diff bool

	for key, value := range src {
		if v, ok := d[key]; ok && v == value {
			continue
		}

		d[key] = value
		diff = true
	}

	return diff
}

func MapCopyF[M ~map[K]V, K, V comparable](src M, dstGet func() M, dstSet func(M)) bool {
	return MapDoF(src, dstGet, dstSet, MapCopy[M, K, V])
}

func MapDoF[M ~map[K]V, K, V comparable, R any](src M, dstGet func() M, dstSet func(M), mapDo func(M, *M) R) R {
	dst := dstGet()
	r := mapDo(src, &dst)
	dstSet(dst)

	return r
}

// Deletes any key-value pairs from dst that are in src.
// Returns whether dst changes.
func MapDelete[M ~map[K]V, K, V comparable](src M, dst *M) bool {
	if *dst == nil {
		return false
	}

	d := *dst

	var diff bool

	for key, value := range src {
		if v, ok := d[key]; ok && v != value {
			continue
		}

		delete(d, key)

		diff = true
	}

	return diff
}

func MapDeleteF[M ~map[K]V, K, V comparable](src M, dstGet func() M, dstSet func(M)) bool {
	return MapDoF(src, dstGet, dstSet, MapDelete[M, K, V])
}

type Comparison int

const (
	Different Comparison = iota
	Same
	Absent
)

// Copies src's key-value pairs into dst only if src's keys are all absent from dst.
// Or, if dst is nil, assigns src to dst.
// Returns state of src's key-value pairs in dst before any changes.
func MapInsertOnlyAll[M ~map[K]V, K, V comparable](src M, dst *M) Comparison {
	if *dst == nil {
		*dst = src

		if src == nil {
			return Same
		}

		return Absent
	}

	d := *dst
	allSame := true

	for key, value := range src {
		v, ok := d[key]
		if !ok {
			allSame = false

			continue
		}

		if v == value {
			continue
		}

		return Different
	}

	if allSame {
		return Same
	}

	maps.Copy(d, src)

	return Absent
}

func MapInsertOnlyAllF[M ~map[K]V, K, V comparable](src M, dstGet func() M, dstSet func(M)) Comparison {
	return MapDoF(src, dstGet, dstSet, MapInsertOnlyAll[M, K, V])
}
