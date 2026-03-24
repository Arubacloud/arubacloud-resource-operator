package controller

import (
	"errors"
	"fmt"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	arubatypes "github.com/Arubacloud/sdk-go/pkg/types"
)

var _ = Describe("CMPErrorCategory", func() {
	DescribeTable("String()",
		func(cat CMPErrorCategory, expected string) {
			Expect(cat.String()).To(Equal(expected))
		},
		Entry("Semantic", CMPErrorCategorySemantic, "semantic"),
		Entry("Technical", CMPErrorCategoryTechnical, "technical"),
		Entry("zero value", CMPErrorCategory(0), "unknown"),
	)
})

var _ = Describe("CMPError", func() {
	Describe("Error()", func() {
		It("formats transport errors with the cause", func() {
			cause := errors.New("connection refused")
			e := &CMPError{
				Category:  CMPErrorCategoryTechnical,
				Operation: "create",
				Resource:  "my-project",
				Cause:     cause,
			}
			Expect(e.Error()).To(Equal("failed to create 'my-project' in Aruba CMP: connection refused"))
		})

		It("formats HTTP status errors with all fields", func() {
			e := &CMPError{
				Category:   CMPErrorCategorySemantic,
				StatusCode: http.StatusBadRequest,
				Title:      "Bad Request",
				Detail:     "quota exceeded",
				Instance:   "urn:uuid:abc",
				Operation:  "create",
				Resource:   "my-project",
			}
			Expect(e.Error()).To(ContainSubstring("failed to create 'my-project' in Aruba CMP"))
			Expect(e.Error()).To(ContainSubstring("status_code: 400"))
			Expect(e.Error()).To(ContainSubstring("category: semantic"))
			Expect(e.Error()).To(ContainSubstring("title: Bad Request"))
			Expect(e.Error()).To(ContainSubstring("detail: quota exceeded"))
			Expect(e.Error()).To(ContainSubstring("instance: urn:uuid:abc"))
		})

		It("formats HTTP status errors with empty fields", func() {
			e := &CMPError{
				Category:   CMPErrorCategoryTechnical,
				StatusCode: http.StatusInternalServerError,
				Operation:  "delete",
				Resource:   "my-vpc",
			}
			Expect(e.Error()).To(ContainSubstring("failed to delete 'my-vpc' in Aruba CMP"))
			Expect(e.Error()).To(ContainSubstring("status_code: 500"))
			Expect(e.Error()).To(ContainSubstring("category: technical"))
		})
	})

	Describe("Unwrap()", func() {
		It("returns nil when Cause is not set", func() {
			e := &CMPError{Category: CMPErrorCategorySemantic}
			Expect(e.Unwrap()).To(BeNil())
		})

		It("returns the Cause error", func() {
			cause := errors.New("dial tcp: connection refused")
			e := &CMPError{Category: CMPErrorCategoryTechnical, Cause: cause}
			Expect(e.Unwrap()).To(Equal(cause))
		})

		It("enables errors.Is on the wrapped chain", func() {
			sentinel := errors.New("sentinel")
			e := &CMPError{Category: CMPErrorCategoryTechnical, Cause: sentinel}
			Expect(errors.Is(e, sentinel)).To(BeTrue())
		})

		It("enables errors.As after fmt.Errorf wrapping", func() {
			inner := &CMPError{Category: CMPErrorCategorySemantic, StatusCode: 400, Operation: "create", Resource: "r"}
			wrapped := fmt.Errorf("outer context: %w", inner)
			var extracted *CMPError
			Expect(errors.As(wrapped, &extracted)).To(BeTrue())
			Expect(extracted.Category).To(Equal(CMPErrorCategorySemantic))
			Expect(extracted.StatusCode).To(Equal(400))
		})
	})
})

var _ = Describe("cmpTransportError", func() {
	It("always sets category to Technical", func() {
		e := cmpTransportError("create", "res", errors.New("timeout"))
		Expect(e.Category).To(Equal(CMPErrorCategoryTechnical))
	})

	It("sets Operation and Resource", func() {
		e := cmpTransportError("delete", "my-vpc", errors.New("err"))
		Expect(e.Operation).To(Equal("delete"))
		Expect(e.Resource).To(Equal("my-vpc"))
	})

	It("preserves the original error as Cause", func() {
		cause := errors.New("network failure")
		e := cmpTransportError("create", "res", cause)
		Expect(errors.Is(e, cause)).To(BeTrue())
	})

	It("sets StatusCode to 0", func() {
		e := cmpTransportError("list", "res", errors.New("err"))
		Expect(e.StatusCode).To(Equal(0))
	})
})

var _ = Describe("cmpResponseError", func() {
	DescribeTable("category classification by status code",
		func(statusCode int, expectedCategory CMPErrorCategory) {
			e := cmpResponseError("create", "res", statusCode, nil)
			Expect(e.Category).To(Equal(expectedCategory))
		},
		Entry("400 Bad Request", http.StatusBadRequest, CMPErrorCategorySemantic),
		Entry("401 Unauthorized", http.StatusUnauthorized, CMPErrorCategorySemantic),
		Entry("403 Forbidden", http.StatusForbidden, CMPErrorCategorySemantic),
		Entry("404 Not Found", http.StatusNotFound, CMPErrorCategorySemantic),
		Entry("409 Conflict", http.StatusConflict, CMPErrorCategorySemantic),
		Entry("422 Unprocessable Entity", http.StatusUnprocessableEntity, CMPErrorCategorySemantic),
		Entry("499 (boundary)", 499, CMPErrorCategorySemantic),
		Entry("500 Internal Server Error", http.StatusInternalServerError, CMPErrorCategoryTechnical),
		Entry("502 Bad Gateway", http.StatusBadGateway, CMPErrorCategoryTechnical),
		Entry("503 Service Unavailable", http.StatusServiceUnavailable, CMPErrorCategoryTechnical),
		Entry("200 (unexpected success passed in)", http.StatusOK, CMPErrorCategoryTechnical),
		Entry("300 (redirect)", http.StatusMultipleChoices, CMPErrorCategoryTechnical),
	)

	It("extracts Title, Detail, Instance from ErrorResponse", func() {
		title, detail, instance := "Bad Request", "quota exceeded", "urn:uuid:abc"
		errResp := &arubatypes.ErrorResponse{
			Title:    &title,
			Detail:   &detail,
			Instance: &instance,
		}
		e := cmpResponseError("create", "res", 400, errResp)
		Expect(e.Title).To(Equal("Bad Request"))
		Expect(e.Detail).To(Equal("quota exceeded"))
		Expect(e.Instance).To(Equal("urn:uuid:abc"))
	})

	It("uses empty strings when ErrorResponse is nil", func() {
		e := cmpResponseError("create", "res", 500, nil)
		Expect(e.Title).To(Equal(""))
		Expect(e.Detail).To(Equal(""))
		Expect(e.Instance).To(Equal(""))
	})

	It("uses empty strings for nil pointer fields in ErrorResponse", func() {
		e := cmpResponseError("create", "res", 400, &arubatypes.ErrorResponse{})
		Expect(e.Title).To(Equal(""))
		Expect(e.Detail).To(Equal(""))
		Expect(e.Instance).To(Equal(""))
	})

	It("sets Operation, Resource, and StatusCode", func() {
		e := cmpResponseError("update", "my-project", 503, nil)
		Expect(e.Operation).To(Equal("update"))
		Expect(e.Resource).To(Equal("my-project"))
		Expect(e.StatusCode).To(Equal(503))
	})

	It("sanitizes tabs and newlines in Title, Detail, and Instance", func() {
		title := "Bad\nRequest\t"
		detail := "quota\n\nexceeded\t(see docs)"
		instance := "urn:\tuuid:\nabc"
		errResp := &arubatypes.ErrorResponse{
			Title:    &title,
			Detail:   &detail,
			Instance: &instance,
		}
		e := cmpResponseError("create", "res", 400, errResp)
		Expect(e.Title).To(Equal("Bad Request"))
		Expect(e.Detail).To(Equal("quota exceeded (see docs)"))
		Expect(e.Instance).To(Equal("urn: uuid: abc"))
	})
})

var _ = Describe("cmpCheckResponse", func() {
	It("returns nil when status code is in successCodes", func() {
		resp := &arubatypes.Response[struct{}]{StatusCode: http.StatusOK}
		Expect(cmpCheckResponse("create", "res", resp, http.StatusOK, http.StatusCreated)).To(Succeed())
	})

	It("returns nil for the second success code", func() {
		resp := &arubatypes.Response[struct{}]{StatusCode: http.StatusCreated}
		Expect(cmpCheckResponse("create", "res", resp, http.StatusOK, http.StatusCreated)).To(Succeed())
	})

	It("returns a CMPError when status code is not in successCodes", func() {
		resp := &arubatypes.Response[struct{}]{StatusCode: http.StatusBadRequest}
		err := cmpCheckResponse("create", "res", resp, http.StatusOK, http.StatusCreated)
		Expect(err).To(HaveOccurred())
		var cmpErr *CMPError
		Expect(errors.As(err, &cmpErr)).To(BeTrue())
		Expect(cmpErr.StatusCode).To(Equal(http.StatusBadRequest))
		Expect(cmpErr.Category).To(Equal(CMPErrorCategorySemantic))
	})

	It("returns a Technical CMPError for 5xx", func() {
		resp := &arubatypes.Response[struct{}]{StatusCode: http.StatusInternalServerError}
		err := cmpCheckResponse("delete", "res", resp, http.StatusNoContent)
		var cmpErr *CMPError
		Expect(errors.As(err, &cmpErr)).To(BeTrue())
		Expect(cmpErr.Category).To(Equal(CMPErrorCategoryTechnical))
	})

	It("extracts ErrorResponse details into the CMPError", func() {
		title := "quota exceeded"
		resp := &arubatypes.Response[struct{}]{
			StatusCode: http.StatusBadRequest,
			Error:      &arubatypes.ErrorResponse{Title: &title},
		}
		err := cmpCheckResponse("create", "res", resp, http.StatusCreated)
		var cmpErr *CMPError
		Expect(errors.As(err, &cmpErr)).To(BeTrue())
		Expect(cmpErr.Title).To(Equal("quota exceeded"))
	})
})

var _ = Describe("CMPErrorIsSemantic", func() {
	It("returns true for a semantic CMPError", func() {
		e := &CMPError{Category: CMPErrorCategorySemantic}
		Expect(CMPErrorIsSemantic(e)).To(BeTrue())
	})

	It("returns false for a technical CMPError", func() {
		e := &CMPError{Category: CMPErrorCategoryTechnical}
		Expect(CMPErrorIsSemantic(e)).To(BeFalse())
	})

	It("returns false for a plain error", func() {
		Expect(CMPErrorIsSemantic(errors.New("some error"))).To(BeFalse())
	})

	It("returns true when the error is wrapped with fmt.Errorf", func() {
		e := &CMPError{Category: CMPErrorCategorySemantic}
		wrapped := fmt.Errorf("outer: %w", e)
		Expect(CMPErrorIsSemantic(wrapped)).To(BeTrue())
	})
})

var _ = Describe("CMPErrorIsTechnical", func() {
	It("returns true for a technical CMPError", func() {
		e := &CMPError{Category: CMPErrorCategoryTechnical}
		Expect(CMPErrorIsTechnical(e)).To(BeTrue())
	})

	It("returns false for a semantic CMPError", func() {
		e := &CMPError{Category: CMPErrorCategorySemantic}
		Expect(CMPErrorIsTechnical(e)).To(BeFalse())
	})

	It("returns false for a plain error", func() {
		Expect(CMPErrorIsTechnical(errors.New("some error"))).To(BeFalse())
	})

	It("returns true when the error is wrapped with fmt.Errorf", func() {
		e := &CMPError{Category: CMPErrorCategoryTechnical}
		wrapped := fmt.Errorf("outer: %w", e)
		Expect(CMPErrorIsTechnical(wrapped)).To(BeTrue())
	})
})
