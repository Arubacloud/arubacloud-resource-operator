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
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Arubacloud/arubacloud-resource-operator/test/utils"
)

var _ = Describe("01-ProjectOnly", Ordered, func() {
	const (
		projectName = "aruba-test-project"
		testTimeout = 20 * time.Minute
	)

	BeforeAll(func() {
		By("ensuring the namespace exists")
		cmd := exec.Command("kubectl", "get", "ns", namespace)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Namespace should exist")
	})

	AfterAll(func() {
		By("cleaning up project resource")
		cmd := exec.Command("kubectl", "delete", "project", projectName, "-n", namespace, "--ignore-not-found=true")
		_, _ = utils.Run(cmd)
	})

	Context("Project Lifecycle", func() {
		It("should create a project successfully", func() {
			By("applying the project manifest")
			projectManifest, err := utils.LoadSampleManifest("arubacloud.com_v1alpha1_project.yaml", map[string]string{
				"__NAME__":      projectName,
				"__NAMESPACE__": namespace,
				"__TENANT__":    "",
			})
			Expect(err).NotTo(HaveOccurred(), "Failed to load project manifest")

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = stringReader(projectManifest)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to apply project manifest")

			By("waiting for project to be created")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "project", projectName, "-n", namespace,
					"-o", "jsonpath={.status.phase}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Created"), "Project should reach Created state")
			}, testTimeout, 5*time.Second).Should(Succeed())

			By("verifying project has a project ID")
			cmd = exec.Command("kubectl", "get", "project", projectName, "-n", namespace,
				"-o", "jsonpath={.status.resourceID}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).NotTo(BeEmpty(), "Project should have a projectID")
		})

		It("should delete the project successfully", func() {
			By("deleting the project")
			cmd := exec.Command("kubectl", "delete", "project", projectName, "-n", namespace)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to delete project")

			By("waiting for project to be fully deleted")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "project", projectName, "-n", namespace)
				_, err := utils.Run(cmd)
				g.Expect(err).To(HaveOccurred(), "Project should be deleted")
			}, testTimeout, 5*time.Second).Should(Succeed())
		})
	})
})
