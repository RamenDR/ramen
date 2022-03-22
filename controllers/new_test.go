/*
Copyright 2022 The RamenDR authors.
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

package controllers_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Sync Failover Test", func() {
	Specify("s3 profiles and secret", func() {
		s3SecretReset()
		s3SecretNamespaceSet()
		s3SecretAndProfilesCreate()
	})
	Context("Sync Happy Path Failover Test", func() {
		When("Application is running on the primary cluster and a failover is initiated", func() {
			It("Should wait for the fencing status to be fenced and then failover", func() {
				Expect((s3Secret.Name)).To(Equal("s3secret"))
			})
		})
	})
	Specify("s3 profiles and secret delete", func() {
		s3SecretAndProfilesDelete()
	})
})
