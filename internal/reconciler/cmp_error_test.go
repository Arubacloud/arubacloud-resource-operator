package reconciler

import (
	"errors"
	"fmt"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Arubacloud/sdk-go/pkg/aruba"
)

var _ = Describe("CMPErrorCategory", func() {
	DescribeTable("String()",
		func(cat CMPErrorCategory, expected string) {
			Expect(cat.String()).To(Equal(expected))
		},
		Entry("Semantic", CMPErrorCategorySemantic, "semantic"),
		Entry("Transient", CMPErrorCategoryTransient, "transient"),
		Entry("Technical", CMPErrorCategoryTechnical, "technical"),
		Entry("zero value", CMPErrorCategory(0), "unknown"),
	)
})

var _ = Describe("CMPError", func() {
	Describe("Error()", func() {
		It("formats transport errors with the cause", func() {
			cause := errors.New("connection refused")
			e := &CMPError{Category: CMPErrorCategoryTechnical, Operation: "create", Resource: "my-project", Cause: cause}
			Expect(e.Error()).To(Equal("failed to create 'my-project' in Aruba CMP: connection refused"))
		})

		It("formats HTTP status errors with all fields", func() {
			e := &CMPError{
				Category: CMPErrorCategorySemantic, StatusCode: http.StatusBadRequest,
				Title: "Bad Request", Detail: "quota exceeded", Instance: "urn:uuid:abc",
				Operation: "create", Resource: "my-project",
			}
			Expect(e.Error()).To(ContainSubstring("failed to create 'my-project' in Aruba CMP"))
			Expect(e.Error()).To(ContainSubstring("status_code: 400"))
			Expect(e.Error()).To(ContainSubstring("category: semantic"))
			Expect(e.Error()).To(ContainSubstring("title: Bad Request"))
			Expect(e.Error()).To(ContainSubstring("detail: quota exceeded"))
			Expect(e.Error()).To(ContainSubstring("instance: urn:uuid:abc"))
		})
	})

	Describe("Unwrap()", func() {
		It("returns nil when Cause is not set", func() {
			Expect((&CMPError{Category: CMPErrorCategorySemantic}).Unwrap()).To(BeNil())
		})

		It("enables errors.Is / errors.As across the wrapped chain", func() {
			sentinel := errors.New("sentinel")
			e := &CMPError{Category: CMPErrorCategoryTechnical, Cause: sentinel}
			Expect(errors.Is(e, sentinel)).To(BeTrue())

			inner := &CMPError{Category: CMPErrorCategorySemantic, StatusCode: 400}
			var extracted *CMPError
			Expect(errors.As(fmt.Errorf("outer: %w", inner), &extracted)).To(BeTrue())
			Expect(extracted.Category).To(Equal(CMPErrorCategorySemantic))
		})
	})
})

var _ = Describe("CMPErrorFromResult", func() {
	It("returns nil when err is nil", func() {
		Expect(CMPErrorFromResult("create", "res", nil)).To(BeNil())
	})

	It("classifies a non-HTTP error as a Technical transport error", func() {
		cause := errors.New("dial tcp: connection refused")
		err := CMPErrorFromResult("create", "my-project", cause)
		var cmpErr *CMPError
		Expect(errors.As(err, &cmpErr)).To(BeTrue())
		Expect(cmpErr.Category).To(Equal(CMPErrorCategoryTechnical))
		Expect(cmpErr.Operation).To(Equal("create"))
		Expect(cmpErr.Resource).To(Equal("my-project"))
		Expect(errors.Is(err, cause)).To(BeTrue()) // cause preserved
	})

	DescribeTable("classifies an *aruba.HTTPError by status code (no field errors)",
		func(statusCode int, expected CMPErrorCategory) {
			err := CMPErrorFromResult("create", "res", &aruba.HTTPError{StatusCode: statusCode})
			var cmpErr *CMPError
			Expect(errors.As(err, &cmpErr)).To(BeTrue())
			Expect(cmpErr.Category).To(Equal(expected))
			Expect(cmpErr.StatusCode).To(Equal(statusCode))
		},
		Entry("400 → transient (no validation body)", http.StatusBadRequest, CMPErrorCategoryTransient),
		Entry("404 → transient", http.StatusNotFound, CMPErrorCategoryTransient),
		Entry("409 → transient", http.StatusConflict, CMPErrorCategoryTransient),
		Entry("500 → technical", http.StatusInternalServerError, CMPErrorCategoryTechnical),
		Entry("503 → technical", http.StatusServiceUnavailable, CMPErrorCategoryTechnical),
	)

	It("treats an okStatusCode as success (nil error)", func() {
		err := CMPErrorFromResult("delete", "res", &aruba.HTTPError{StatusCode: http.StatusNotFound}, http.StatusNotFound)
		Expect(err).To(BeNil())
	})

	It("still fails for a non-ok status even when okStatusCodes are given", func() {
		err := CMPErrorFromResult("delete", "res", &aruba.HTTPError{StatusCode: http.StatusBadRequest}, http.StatusNotFound)
		Expect(err).To(HaveOccurred())
	})

	It("classifies a wrapped *aruba.HTTPError", func() {
		wrapped := fmt.Errorf("outer: %w", &aruba.HTTPError{StatusCode: http.StatusInternalServerError})
		err := CMPErrorFromResult("update", "res", wrapped)
		Expect(CMPErrorIsTechnical(err)).To(BeTrue())
	})
})

var _ = Describe("CMPError category predicates", func() {
	DescribeTable("CMPErrorIsSemantic / Transient / Technical",
		func(cat CMPErrorCategory, semantic, transient, technical bool) {
			e := &CMPError{Category: cat}
			Expect(CMPErrorIsSemantic(e)).To(Equal(semantic))
			Expect(CMPErrorIsTransient(e)).To(Equal(transient))
			Expect(CMPErrorIsTechnical(e)).To(Equal(technical))
		},
		Entry("semantic", CMPErrorCategorySemantic, true, false, false),
		Entry("transient", CMPErrorCategoryTransient, false, true, false),
		Entry("technical", CMPErrorCategoryTechnical, false, false, true),
	)

	It("all predicates return false for a plain error", func() {
		e := errors.New("some error")
		Expect(CMPErrorIsSemantic(e)).To(BeFalse())
		Expect(CMPErrorIsTransient(e)).To(BeFalse())
		Expect(CMPErrorIsTechnical(e)).To(BeFalse())
	})

	It("predicates see through fmt.Errorf wrapping", func() {
		e := &CMPError{Category: CMPErrorCategorySemantic}
		Expect(CMPErrorIsSemantic(fmt.Errorf("outer: %w", e))).To(BeTrue())
	})
})
