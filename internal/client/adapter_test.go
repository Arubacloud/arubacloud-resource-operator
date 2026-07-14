package client

import (
	"errors"
	"net/http"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Arubacloud/sdk-go/pkg/aruba"
	"github.com/Arubacloud/sdk-go/pkg/types"
)

func TestClient(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Client Port Suite")
}

// fakeEnvelope implements wireEnvelope so objResp can be exercised without a
// live SDK call. It uses a pointer receiver so a typed-nil value panics if
// dereferenced — the regression guard for the Update hydration path.
type fakeEnvelope[R any] struct {
	status  int
	errResp *types.ErrorResponse
	raw     *R
}

func (e *fakeEnvelope[R]) StatusCode() int                { return e.status }
func (e *fakeEnvelope[R]) RawError() *types.ErrorResponse { return e.errResp }
func (e *fakeEnvelope[R]) Raw() *R                        { return e.raw }

func httpErr(status int, errResp *types.ErrorResponse) error {
	return &aruba.HTTPError{StatusCode: status, ErrResp: errResp}
}

var _ = Describe("classify", func() {
	It("returns zero values for a nil error", func() {
		status, errResp, transport := classify(nil)
		Expect(status).To(Equal(0))
		Expect(errResp).To(BeNil())
		Expect(transport).To(BeNil())
	})

	It("surfaces an *aruba.HTTPError as status + errResp, not a transport error", func() {
		er := &types.ErrorResponse{}
		status, errResp, transport := classify(httpErr(http.StatusBadRequest, er))
		Expect(status).To(Equal(http.StatusBadRequest))
		Expect(errResp).To(BeIdenticalTo(er))
		Expect(transport).To(BeNil())
	})

	It("returns any other error as a transport failure", func() {
		boom := errors.New("boom")
		status, errResp, transport := classify(boom)
		Expect(status).To(Equal(0))
		Expect(errResp).To(BeNil())
		Expect(transport).To(MatchError(boom))
	})
})

var _ = Describe("objResp", func() {
	It("copies the envelope on success", func() {
		data := &types.ProjectResponse{}
		w := &fakeEnvelope[types.ProjectResponse]{status: http.StatusOK, raw: data}
		resp, err := objResp[types.ProjectResponse](w, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(resp.Data).To(BeIdenticalTo(data))
	})

	It("overlays an HTTP error's status and errResp onto the response", func() {
		er := &types.ErrorResponse{}
		w := &fakeEnvelope[types.ProjectResponse]{status: http.StatusOK, raw: &types.ProjectResponse{}}
		resp, err := objResp[types.ProjectResponse](w, httpErr(http.StatusConflict, er))
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusConflict))
		Expect(resp.Error).To(BeIdenticalTo(er))
	})

	It("returns the transport error to the caller", func() {
		boom := errors.New("boom")
		w := &fakeEnvelope[types.ProjectResponse]{status: http.StatusOK}
		_, err := objResp[types.ProjectResponse](w, boom)
		Expect(err).To(MatchError(boom))
	})

	It("does not dereference a typed-nil wrapper on a transport error", func() {
		var w *fakeEnvelope[types.ProjectResponse] // typed nil boxed into the interface
		var resp *types.Response[types.ProjectResponse]
		var err error
		Expect(func() {
			resp, err = objResp[types.ProjectResponse](w, errors.New("ref parse failed"))
		}).NotTo(Panic())
		Expect(err).To(MatchError("ref parse failed"))
		Expect(resp).NotTo(BeNil())
	})
})

var _ = Describe("listResp", func() {
	It("returns a non-nil empty Data on success even with a nil list", func() {
		resp, err := listResp[types.ProjectListResponse, *aruba.Project](nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Data).NotTo(BeNil())
	})

	It("returns the transport error and leaves Data nil", func() {
		boom := errors.New("boom")
		resp, err := listResp[types.ProjectListResponse, *aruba.Project](nil, boom)
		Expect(err).To(MatchError(boom))
		Expect(resp.Data).To(BeNil())
	})

	It("surfaces an HTTP error's status and errResp", func() {
		er := &types.ErrorResponse{}
		resp, err := listResp[types.ProjectListResponse, *aruba.Project](nil, httpErr(http.StatusInternalServerError, er))
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusInternalServerError))
		Expect(resp.Error).To(BeIdenticalTo(er))
	})
})

var _ = Describe("rawList", func() {
	It("returns nil data for a nil raw payload (legitimately empty)", func() {
		data, err := rawList[types.ProjectListResponse](nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(BeNil())
	})

	It("returns the typed payload when the raw value matches", func() {
		want := &types.ProjectListResponse{}
		data, err := rawList[types.ProjectListResponse](want)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(BeIdenticalTo(want))
	})

	It("errors on a non-nil raw payload of the wrong type instead of dropping it", func() {
		data, err := rawList[types.ProjectListResponse](&types.VPCListResponse{})
		Expect(data).To(BeNil())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("want *types.ProjectListResponse"))
	})
})

var _ = Describe("deleteResp", func() {
	It("maps a nil error to 200 OK", func() {
		resp, err := deleteResp(nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
	})

	It("maps an HTTP error to its status without returning an error", func() {
		resp, err := deleteResp(httpErr(http.StatusNotFound, nil))
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
	})

	It("returns a transport error to the caller", func() {
		boom := errors.New("boom")
		_, err := deleteResp(boom)
		Expect(err).To(MatchError(boom))
	})
})
