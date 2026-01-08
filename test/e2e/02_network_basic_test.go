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

var _ = Describe("02-NetworkBasic", Ordered, func() {
	const (
		projectName = "aruba-test-network-basic"
		vpcName     = "aruba-test-network-basic"
		subnetName  = "aruba-test-network-basic"
		testTimeout = 20 * time.Minute
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
			{"subnet", subnetName},
			{"vpc", vpcName},
			{"project", projectName},
		}

		for _, res := range resources {
			cmd := exec.Command("kubectl", "delete", res.kind, res.name, "-n", namespace, "--ignore-not-found=true", "--timeout=5m")
			_, _ = utils.Run(cmd)
		}
	})

	Context("Network Basic Stack", func() {
		It("should create project, VPC, and subnet successfully", func() {
			By("applying the project manifest")
			projectManifest, err := utils.LoadSampleManifest("arubacloud.com_v1alpha1_project.yaml", map[string]string{
				"__NAME__":      projectName,
				"__NAMESPACE__": namespace,
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
				g.Expect(output).To(Equal("Created"))
			}, testTimeout, 5*time.Second).Should(Succeed())

			By("applying the VPC manifest")
			vpcManifest, err := utils.LoadSampleManifest("arubacloud.com_v1alpha1_vpc.yaml", map[string]string{
				"__NAME__":      vpcName,
				"__NAMESPACE__": namespace,
			})
			Expect(err).NotTo(HaveOccurred(), "Failed to load VPC manifest")

			cmd = exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = stringReader(vpcManifest)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to apply VPC manifest")

			By("waiting for VPC to be created")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "vpc", vpcName, "-n", namespace,
					"-o", "jsonpath={.status.phase}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Created"))
			}, testTimeout, 5*time.Second).Should(Succeed())

			By("applying the Subnet manifest")
			subnetManifest, err := utils.LoadSampleManifest("arubacloud.com_v1alpha1_subnet.yaml", map[string]string{
				"__NAME__":      subnetName,
				"__NAMESPACE__": namespace,
			})
			Expect(err).NotTo(HaveOccurred(), "Failed to load Subnet manifest")

			cmd = exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = stringReader(subnetManifest)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to apply Subnet manifest")

			By("waiting for Subnet to be created")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "subnet", subnetName, "-n", namespace,
					"-o", "jsonpath={.status.phase}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Created"))
			}, testTimeout, 5*time.Second).Should(Succeed())

			By("verifying all resources have proper status IDs")
			cmd = exec.Command("kubectl", "get", "project", projectName, "-n", namespace,
				"-o", "jsonpath={.status.resourceID}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).NotTo(BeEmpty(), "Project should have resourceID")

			cmd = exec.Command("kubectl", "get", "vpc", vpcName, "-n", namespace,
				"-o", "jsonpath={.status.resourceID}")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).NotTo(BeEmpty(), "VPC should have resourceID")

			cmd = exec.Command("kubectl", "get", "subnet", subnetName, "-n", namespace,
				"-o", "jsonpath={.status.resourceID}")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).NotTo(BeEmpty(), "Subnet should have resourceID")
		})
	})
})
