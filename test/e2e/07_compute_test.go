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

var _ = Describe("07-Compute", Ordered, func() {
	const (
		name        = "aruba-test-compute-eip"
		namespace   = "default"
		testTimeout = 20 * time.Minute
	)

	BeforeAll(func() {
		By("ensuring the namespace exists")
		cmd := exec.Command("kubectl", "get", "ns", namespace)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "namespace should exist")
	})

	AfterAll(func() {
		By("cleaning up resources in reverse order")

		By("deleting CloudServer")
		cmd := exec.Command("kubectl", "delete", "cloudserver", name, "-n", namespace, "--ignore-not-found")
		_, _ = utils.Run(cmd)

		By("deleting KeyPair")
		cmd = exec.Command("kubectl", "delete", "keypair", name, "-n", namespace, "--ignore-not-found")
		_, _ = utils.Run(cmd)

		By("deleting ElasticIP")
		cmd = exec.Command("kubectl", "delete", "elasticip", name, "-n", namespace, "--ignore-not-found")
		_, _ = utils.Run(cmd)

		By("deleting BlockStorage")
		cmd = exec.Command("kubectl", "delete", "blockstorage", name, "-n", namespace, "--ignore-not-found")
		_, _ = utils.Run(cmd)

		By("deleting Subnet")
		cmd = exec.Command("kubectl", "delete", "subnet", name, "-n", namespace, "--ignore-not-found")
		_, _ = utils.Run(cmd)

		By("deleting SecurityRule")
		cmd = exec.Command("kubectl", "delete", "securityrule", name, "-n", namespace, "--ignore-not-found")
		_, _ = utils.Run(cmd)

		By("deleting SecurityGroup")
		cmd = exec.Command("kubectl", "delete", "securitygroup", name, "-n", namespace, "--ignore-not-found")
		_, _ = utils.Run(cmd)

		By("deleting VPC")
		cmd = exec.Command("kubectl", "delete", "vpc", name, "-n", namespace, "--ignore-not-found")
		_, _ = utils.Run(cmd)

		By("deleting Project")
		cmd = exec.Command("kubectl", "delete", "project", name, "-n", namespace, "--ignore-not-found")
		_, _ = utils.Run(cmd)
	})

	It("should create all resources and CloudServer with ElasticIP", func(ctx SpecContext) {

		// Load all manifests
		projectManifest, err := utils.LoadSampleManifest("arubacloud.com_v1alpha1_project.yaml", map[string]string{
			"__NAME__":      name,
			"__NAMESPACE__": namespace,
		})
		Expect(err).NotTo(HaveOccurred())

		vpcManifest, err := utils.LoadSampleManifest("arubacloud.com_v1alpha1_vpc.yaml", map[string]string{
			"__NAME__":      name,
			"__NAMESPACE__": namespace,
		})
		Expect(err).NotTo(HaveOccurred())

		sgManifest, err := utils.LoadSampleManifest("arubacloud.com_v1alpha1_securitygroup.yaml", map[string]string{
			"__NAME__":      name,
			"__NAMESPACE__": namespace,
		})
		Expect(err).NotTo(HaveOccurred())

		ruleManifest, err := utils.LoadSampleManifest("arubacloud.com_v1alpha1_securityrule.yaml", map[string]string{
			"__NAME__":      name,
			"__NAMESPACE__": namespace,
		})
		Expect(err).NotTo(HaveOccurred())

		elasticIPManifest, err := utils.LoadSampleManifest("arubacloud.com_v1alpha1_elasticip.yaml", map[string]string{
			"__NAME__":      name,
			"__NAMESPACE__": namespace,
		})
		Expect(err).NotTo(HaveOccurred())

		subnetManifest, err := utils.LoadSampleManifest("arubacloud.com_v1alpha1_subnet.yaml", map[string]string{
			"__NAME__":      name,
			"__NAMESPACE__": namespace,
		})
		Expect(err).NotTo(HaveOccurred())

		blockStorageManifest, err := utils.LoadSampleManifest("arubacloud.com_v1alpha1_blockstorage_bootable.yaml", map[string]string{
			"__NAME__":              name,
			"__NAMESPACE__":         namespace,
			"__PROJECT_NAME__":      name,
			"__PROJECT_NAMESPACE__": namespace,
		})
		Expect(err).NotTo(HaveOccurred())

		keyPairManifest, err := utils.LoadSampleManifest("arubacloud.com_v1alpha1_keypair.yaml", map[string]string{
			"__NAME__":      name,
			"__NAMESPACE__": namespace,
		})
		Expect(err).NotTo(HaveOccurred())

		cloudServerManifest, err := utils.LoadSampleManifest("arubacloud.com_v1alpha1_cloudserver.yaml", map[string]string{
			"__NAME__":      name,
			"__NAMESPACE__": namespace,
		})
		Expect(err).NotTo(HaveOccurred())

		// Combine all manifests
		allManifests := projectManifest + "\n---\n" +
			vpcManifest + "\n---\n" +
			sgManifest + "\n---\n" +
			ruleManifest + "\n---\n" +
			elasticIPManifest + "\n---\n" +
			subnetManifest + "\n---\n" +
			blockStorageManifest + "\n---\n" +
			keyPairManifest + "\n---\n" +
			cloudServerManifest

		By("applying all manifests at once")
		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = stringReader(allManifests)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())

		By("waiting for CloudServer to be Created")
		Eventually(func() string {
			cmd := exec.Command("kubectl", "get", "cloudserver", name, "-n", namespace, "-o", "jsonpath={.status.phase}")
			output, _ := utils.Run(cmd)
			return output
		}, testTimeout, 10*time.Second).Should(Equal("Created"))

		By("verifying CloudServer has resourceID")
		cmd = exec.Command("kubectl", "get", "cloudserver", name, "-n", namespace, "-o", "jsonpath={.status.resourceID}")
		output, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).NotTo(BeEmpty())

		By("verifying CloudServer has projectID")
		cmd = exec.Command("kubectl", "get", "cloudserver", name, "-n", namespace, "-o", "jsonpath={.status.projectID}")
		output, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).NotTo(BeEmpty())

		By("verifying CloudServer has vpcID")
		cmd = exec.Command("kubectl", "get", "cloudserver", name, "-n", namespace, "-o", "jsonpath={.status.vpcID}")
		output, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).NotTo(BeEmpty())

		By("verifying CloudServer has bootVolumeID")
		cmd = exec.Command("kubectl", "get", "cloudserver", name, "-n", namespace, "-o", "jsonpath={.status.bootVolumeID}")
		output, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).NotTo(BeEmpty())

		By("verifying CloudServer has elasticIpID")
		cmd = exec.Command("kubectl", "get", "cloudserver", name, "-n", namespace, "-o", "jsonpath={.status.elasticIpID}")
		output, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).NotTo(BeEmpty())

		By("verifying CloudServer has keyPairID")
		cmd = exec.Command("kubectl", "get", "cloudserver", name, "-n", namespace, "-o", "jsonpath={.status.keyPairID}")
		output, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).NotTo(BeEmpty())

		By("verifying CloudServer has subnetIDs")
		cmd = exec.Command("kubectl", "get", "cloudserver", name, "-n", namespace, "-o", "jsonpath={.status.subnetIDs}")
		output, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).NotTo(BeEmpty())

		By("verifying CloudServer has securityGroupIDs")
		cmd = exec.Command("kubectl", "get", "cloudserver", name, "-n", namespace, "-o", "jsonpath={.status.securityGroupIDs}")
		output, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).NotTo(BeEmpty())
	}, SpecTimeout(testTimeout))
})
