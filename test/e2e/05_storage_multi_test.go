/*
Copyright 2025.

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

package e2e

import (
	"fmt"
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Arubacloud/arubacloud-resource-operator/test/utils"
)

var _ = Describe("05-StorageMulti", Ordered, func() {
	const (
		projectName          = "aruba-test-storage-multi"
		blockStorageBootName = "aruba-test-storage-boot"
		blockStorageDataName = "aruba-test-storage-data"
		testTimeout          = 20 * time.Minute
	)

	BeforeAll(func() {
		By("ensuring the namespace exists")
		cmd := exec.Command("kubectl", "get", "ns", namespace)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Namespace should exist")
	})

	AfterAll(func() {
		By("cleaning up resources in reverse order")
		resources := []struct {
			kind string
			name string
		}{
			{"blockstorage", blockStorageDataName},
			{"blockstorage", blockStorageBootName},
			{"project", projectName},
		}

		for _, res := range resources {
			cmd := exec.Command("kubectl", "delete", res.kind, res.name, "-n", namespace, "--ignore-not-found=true", "--timeout=5m")
			_, _ = utils.Run(cmd)
		}
	})

	Context("Storage Multiple Volumes", func() {
		It("should create project with boot and data volumes", func() {
			By("applying the project manifest")
			projectManifest, err := utils.LoadSampleManifest("arubacloud.com_v1alpha1_project.yaml", map[string]string{
				"__NAME__":      projectName,
				"__NAMESPACE__": namespace,
			})
			Expect(err).NotTo(HaveOccurred())

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = stringReader(projectManifest)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for project to be created")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "project", projectName, "-n", namespace,
					"-o", "jsonpath={.status.phase}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Created"))
			}, testTimeout, 5*time.Second).Should(Succeed())

			By("applying the boot BlockStorage manifest")
			bsBootManifest, err := utils.LoadSampleManifest("arubacloud.com_v1alpha1_blockstorage_bootable.yaml", map[string]string{
				"__NAME__":              blockStorageBootName,
				"__NAMESPACE__":         namespace,
				"__PROJECT_NAME__":      projectName,
				"__PROJECT_NAMESPACE__": namespace,
			})
			Expect(err).NotTo(HaveOccurred())

			cmd = exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = stringReader(bsBootManifest)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("applying the data BlockStorage manifest")
			bsDataManifest, err := utils.LoadSampleManifest("arubacloud.com_v1alpha1_blockstorage-data-volume.yaml", map[string]string{
				"__NAME__":              blockStorageDataName,
				"__NAMESPACE__":         namespace,
				"__PROJECT_NAME__":      projectName,
				"__PROJECT_NAMESPACE__": namespace,
			})
			Expect(err).NotTo(HaveOccurred())

			cmd = exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = stringReader(bsDataManifest)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for all BlockStorages to be created")
			storages := []string{blockStorageBootName, blockStorageDataName}
			for _, storageName := range storages {
				Eventually(func(g Gomega) {
					cmd := exec.Command("kubectl", "get", "blockstorage", storageName, "-n", namespace,
						"-o", "jsonpath={.status.phase}")
					output, err := utils.Run(cmd)
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(output).To(Equal("Created"))
				}, testTimeout, 5*time.Second).Should(Succeed())
			}

			By("verifying all BlockStorages have volume IDs")
			for _, storageName := range storages {
				cmd = exec.Command("kubectl", "get", "blockstorage", storageName, "-n", namespace,
					"-o", "jsonpath={.status.resourceID}")
				output, err := utils.Run(cmd)
				Expect(err).NotTo(HaveOccurred())
				Expect(output).NotTo(BeEmpty(), fmt.Sprintf("BlockStorage %s should have resourceID", storageName))
			}
		})
	})
})
