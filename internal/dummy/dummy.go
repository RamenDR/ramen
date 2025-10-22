// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

// package dummy is a workaround for golangci-lint gci formatter limitation. It
// is used to provide a dummy import to preserve the special
// `// +kubebuilder:scaffold:imports` comment:
//
//	import (
//
//		// +kubebuilder:scaffold:imports
//		_ "github.com/ramendr/ramen/internal/dummy"
//	)
//
// When the kubebuilder comment is before an import line it is not considered a
// standalone comment and is preserved when gci reformat the imports.
package dummy
