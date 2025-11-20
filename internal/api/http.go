package signerapi

import (
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"

	signerv1 "github.com/aegis-sign/wallet/docs/api/gen/go"
	"github.com/aegis-sign/wallet/pkg/apierrors"
	"github.com/aegis-sign/wallet/pkg/validator"
)

// HTTPHandler 实现 `/create` `/sign` HTTP/JSON 接口。
type HTTPHandler struct {
	backend Backend
}

// NewHTTPHandler 构造 HTTP handler。
func NewHTTPHandler(backend Backend) *HTTPHandler {
	if backend == nil {
		panic("signer backend is required")
	}
	return &HTTPHandler{backend: backend}
}

// Register 将 handler 注册到 mux。
func (h *HTTPHandler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/create", h.handleCreate)
	mux.HandleFunc("/sign", h.handleSign)
}

type auditHeaders struct {
	RequestID string `json:"requestId"`
	TenantID  string `json:"tenantId"`
}

type createRequestBody struct {
	Curve        string        `json:"curve"`
	AuditHeaders *auditHeaders `json:"auditHeaders"`
}

type createResponseBody struct {
	KeyID     string `json:"keyId"`
	PublicKey string `json:"publicKey"`
	Address   string `json:"address,omitempty"`
}

type signRequestBody struct {
	KeyID        string        `json:"keyId"`
	Digest       string        `json:"digest"`
	Encoding     string        `json:"encoding"`
	AuditHeaders *auditHeaders `json:"auditHeaders"`
}

type signResponseBody struct {
	Signature string  `json:"signature"`
	RecID     *uint32 `json:"recId,omitempty"`
}

type errorResponse struct {
	Code           string `json:"code"`
	Message        string `json:"message"`
	RetryAfterHint string `json:"retryAfterHint,omitempty"`
}

func (h *HTTPHandler) handleCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.writeAPIError(w, apierrors.New(apierrors.CodeInvalidArgument, "POST required"))
		return
	}
	var body createRequestBody
	if r.Body != nil && r.Body != http.NoBody {
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&body); err != nil && err != io.EOF {
			h.writeAPIError(w, apierrors.New(apierrors.CodeInvalidArgument, "invalid JSON body"))
			return
		}
	}
	resp, err := h.backend.Create(r.Context(), &signerv1.CreateRequest{
		Curve:        body.Curve,
		AuditContext: convertAuditHeaders(body.AuditHeaders),
	})
	if err != nil {
		h.writeUnknownError(w, err)
		return
	}
	publicKey := hex.EncodeToString(resp.GetPublicKey())
	payload := createResponseBody{
		KeyID:     resp.GetKeyId(),
		PublicKey: publicKey,
		Address:   resp.GetAddress(),
	}
	h.writeJSON(w, http.StatusOK, payload)
}

func (h *HTTPHandler) handleSign(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.writeAPIError(w, apierrors.New(apierrors.CodeInvalidArgument, "POST required"))
		return
	}
	var body signRequestBody
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&body); err != nil {
		h.writeAPIError(w, apierrors.New(apierrors.CodeInvalidArgument, "invalid JSON body"))
		return
	}
	if body.KeyID == "" {
		h.writeAPIError(w, apierrors.New(apierrors.CodeInvalidArgument, "keyId is required"))
		return
	}
	if body.Digest == "" {
		h.writeAPIError(w, apierrors.New(apierrors.CodeInvalidArgument, "digest is required"))
		return
	}
	encoding, err := validator.NormalizeEncoding(body.Encoding)
	if err != nil {
		h.writeAPIError(w, apierrors.New(apierrors.CodeInvalidArgument, err.Error()))
		return
	}
	decoded, err := validator.DecodeDigest(body.Digest, encoding)
	if err != nil {
		h.writeAPIError(w, apierrors.New(apierrors.CodeInvalidArgument, err.Error()))
		return
	}
	resp, err := h.backend.Sign(r.Context(), &signerv1.SignRequest{
		KeyId:        body.KeyID,
		Digest:       decoded,
		Encoding:     convertEncoding(encoding),
		AuditContext: convertAuditHeaders(body.AuditHeaders),
	})
	if err != nil {
		h.writeUnknownError(w, err)
		return
	}
	payload := signResponseBody{Signature: hex.EncodeToString(resp.GetSignature())}
	if resp.GetRecId() != 0 {
		value := resp.GetRecId()
		payload.RecID = &value
	}
	h.writeJSON(w, http.StatusOK, payload)
}

func (h *HTTPHandler) writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (h *HTTPHandler) writeUnknownError(w http.ResponseWriter, err error) {
	if apiErr, ok := apierrors.FromError(err); ok {
		h.writeAPIError(w, apiErr)
		return
	}
	h.writeAPIError(w, apierrors.New(apierrors.Code("INTERNAL_ERROR"), "internal error"))
}

func (h *HTTPHandler) writeAPIError(w http.ResponseWriter, apiErr *apierrors.Error) {
	if apiErr == nil {
		apiErr = apierrors.New(apierrors.Code("INTERNAL_ERROR"), "internal error")
	}
	status := apierrors.HTTPStatus(apiErr.Code)
	if status == 0 {
		status = http.StatusInternalServerError
	}
	if apierrors.RequiresRetryAfter(apiErr.Code) {
		if hint := apiErr.RetryAfterHint(); hint != "" {
			w.Header().Set("Retry-After", hint)
		}
	}
	resp := errorResponse{
		Code:    string(apiErr.Code),
		Message: apiErr.Error(),
	}
	if hint := apiErr.RetryAfterHint(); hint != "" {
		resp.RetryAfterHint = hint
	}
	h.writeJSON(w, status, resp)
}

func convertEncoding(enc validator.DigestEncoding) signerv1.DigestEncoding {
	switch enc {
	case validator.DigestEncodingBase64:
		return signerv1.DigestEncoding_DIGEST_ENCODING_BASE64
	default:
		return signerv1.DigestEncoding_DIGEST_ENCODING_HEX
	}
}

func convertAuditHeaders(headers *auditHeaders) *signerv1.AuditContext {
	if headers == nil {
		return nil
	}
	if headers.RequestID == "" && headers.TenantID == "" {
		return nil
	}
	return &signerv1.AuditContext{
		RequestId: headers.RequestID,
		TenantId:  headers.TenantID,
	}
}
