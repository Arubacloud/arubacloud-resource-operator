package reconciler

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
)

// testBundle is a minimal bundle type for validation tests.
type testBundle struct {
	Region string
	Zone   string
	Tenant string
}

// testCMPObj is a minimal CMP response substitute for validation tests.
type testCMPObj struct {
	Region string
	Zone   string
}

var _ = Describe("ErrInvalid", func() {
	Describe("Error()", func() {
		It("formats a single violation correctly", func() {
			err := &ErrInvalid{Violations: []ValidationViolation{
				{Rule: "TenantMustMatchProject", Message: "tenant mismatch with Project: \"t1\" != \"t2\""},
			}}
			Expect(err.Error()).To(Equal(
				`validation failed: [TenantMustMatchProject] tenant mismatch with Project: "t1" != "t2"`,
			))
		})

		It("formats multiple violations separated by '; '", func() {
			err := &ErrInvalid{Violations: []ValidationViolation{
				{Rule: "TenantMustMatchProject", Message: "tenant mismatch with Project: \"t1\" != \"t2\""},
				{Rule: "TenantMustMatchVPC", Message: "tenant mismatch with VPC: \"t1\" != \"t2\""},
			}}
			Expect(err.Error()).To(ContainSubstring("[TenantMustMatchProject]"))
			Expect(err.Error()).To(ContainSubstring("[TenantMustMatchVPC]"))
			Expect(err.Error()).To(ContainSubstring("; "))
		})
	})

	Describe("Unwrap()", func() {
		It("returns individual errors for each violation", func() {
			err := &ErrInvalid{Violations: []ValidationViolation{
				{Rule: "RuleA", Message: "msg-a"},
				{Rule: "RuleB", Message: "msg-b"},
			}}
			unwrapped := err.Unwrap()
			Expect(unwrapped).To(HaveLen(2))
			Expect(unwrapped[0].Error()).To(ContainSubstring("RuleA"))
			Expect(unwrapped[1].Error()).To(ContainSubstring("RuleB"))
		})
	})
})

var _ = Describe("IsErrInvalid", func() {
	It("returns true for a direct *ErrInvalid", func() {
		err := &ErrInvalid{Violations: []ValidationViolation{{Rule: "R", Message: "m"}}}
		Expect(IsErrInvalid(err)).To(BeTrue())
	})

	It("returns true for a wrapped *ErrInvalid", func() {
		inner := &ErrInvalid{Violations: []ValidationViolation{{Rule: "R", Message: "m"}}}
		wrapped := errors.Join(errors.New("outer"), inner)
		Expect(IsErrInvalid(wrapped)).To(BeTrue())
	})

	It("returns false for a non-ErrInvalid error", func() {
		Expect(IsErrInvalid(errors.New("plain error"))).To(BeFalse())
	})

	It("returns false for nil", func() {
		Expect(IsErrInvalid(nil)).To(BeFalse())
	})
})

var _ = Describe("ValidationSet", func() {
	var (
		kubeObj *v1alpha1.Project
		cmpObj  *testCMPObj
		bundle  *testBundle
	)

	BeforeEach(func() {
		kubeObj = &v1alpha1.Project{}
		cmpObj = &testCMPObj{Region: "ITBG-Bergamo", Zone: "ITBG-1"}
		bundle = &testBundle{Region: "ITBG-Bergamo", Zone: "ITBG-1", Tenant: "tenant-a"}
	})

	It("returns nil when no validations are registered", func() {
		vs := &ValidationSet[*v1alpha1.Project, *testCMPObj, *testBundle]{}
		Expect(vs.Run(kubeObj, cmpObj, bundle)).To(Succeed())
	})

	It("returns nil when all validations pass", func() {
		vs := &ValidationSet[*v1alpha1.Project, *testCMPObj, *testBundle]{}
		vs.Add("AlwaysPass", func(_ *v1alpha1.Project, _ *testCMPObj, _ *testBundle) error {
			return nil
		})
		Expect(vs.Run(kubeObj, cmpObj, bundle)).To(Succeed())
	})

	It("returns ErrInvalid with a single violation when one fails", func() {
		vs := &ValidationSet[*v1alpha1.Project, *testCMPObj, *testBundle]{}
		vs.Add("AlwaysFail", func(_ *v1alpha1.Project, _ *testCMPObj, _ *testBundle) error {
			return errors.New("always fails")
		})

		err := vs.Run(kubeObj, cmpObj, bundle)
		Expect(err).To(HaveOccurred())
		Expect(IsErrInvalid(err)).To(BeTrue())

		var invalid *ErrInvalid
		Expect(errors.As(err, &invalid)).To(BeTrue())
		Expect(invalid.Violations).To(HaveLen(1))
		Expect(invalid.Violations[0].Rule).To(Equal("AlwaysFail"))
	})

	It("collects all violations when multiple validations fail", func() {
		vs := &ValidationSet[*v1alpha1.Project, *testCMPObj, *testBundle]{}
		vs.Add("Fail1", func(_ *v1alpha1.Project, _ *testCMPObj, _ *testBundle) error {
			return errors.New("first failure")
		})
		vs.Add("Pass1", func(_ *v1alpha1.Project, _ *testCMPObj, _ *testBundle) error {
			return nil
		})
		vs.Add("Fail2", func(_ *v1alpha1.Project, _ *testCMPObj, _ *testBundle) error {
			return errors.New("second failure")
		})

		err := vs.Run(kubeObj, cmpObj, bundle)
		Expect(IsErrInvalid(err)).To(BeTrue())

		var invalid *ErrInvalid
		Expect(errors.As(err, &invalid)).To(BeTrue())
		Expect(invalid.Violations).To(HaveLen(2))
		Expect(invalid.Violations[0].Rule).To(Equal("Fail1"))
		Expect(invalid.Violations[1].Rule).To(Equal("Fail2"))
	})
})

var _ = Describe("FieldMustMatch", func() {
	It("returns nil when values match", func() {
		fn := FieldMustMatch[*v1alpha1.Project, *testCMPObj, *testBundle](
			"tenant",
			func(k *v1alpha1.Project) string { return k.Spec.Tenant },
			func(b *testBundle) string { return b.Tenant },
			"Project",
		)
		kubeObj := &v1alpha1.Project{Spec: v1alpha1.ProjectSpec{Tenant: "tenant-a"}}
		err := fn(kubeObj, &testCMPObj{}, &testBundle{Tenant: "tenant-a"})
		Expect(err).To(Succeed())
	})

	It("returns an error when values differ", func() {
		fn := FieldMustMatch[*v1alpha1.Project, *testCMPObj, *testBundle](
			"tenant",
			func(k *v1alpha1.Project) string { return k.Spec.Tenant },
			func(b *testBundle) string { return b.Tenant },
			"Project",
		)
		kubeObj := &v1alpha1.Project{Spec: v1alpha1.ProjectSpec{Tenant: "tenant-a"}}
		err := fn(kubeObj, &testCMPObj{}, &testBundle{Tenant: "tenant-b"})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("tenant mismatch with Project"))
		Expect(err.Error()).To(ContainSubstring("tenant-a"))
		Expect(err.Error()).To(ContainSubstring("tenant-b"))
	})
})
