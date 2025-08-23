package drivefs

import (
	"io/fs"
	"net/http"
	"net/url"
	"reflect"

	"github.com/googleapis/gax-go/v2/apierror"
	"golang.org/x/net/http2"
	"google.golang.org/api/googleapi"
)

// Process response error and return equivalent to fs or os error
func ProcessErr(res *googleapi.ServerResponse, err error) error {
	if res != nil {
		switch res.HTTPStatusCode {
		case http.StatusNotFound:
			return fs.ErrNotExist
		case http.StatusBadRequest, http.StatusForbidden:
			return fs.ErrInvalid
		case http.StatusTooManyRequests, http.StatusUnauthorized:
			return fs.ErrPermission
		case http.StatusInternalServerError:
			return fs.ErrInvalid
		case http.StatusConflict:
			return fs.ErrExist
		}
	}

	urlErr := (*url.Error)(nil)
	switch v := err.(type) {
	case *url.Error:
		urlErr = v
		err = v.Err
	case *apierror.APIError:
		if details := v.Details(); details.QuotaFailure != nil {
			return fs.ErrPermission
		}
		return ProcessErr(&googleapi.ServerResponse{HTTPStatusCode: v.HTTPCode(), Header: nil}, v.Unwrap())
	case *googleapi.Error:
		return ProcessErr(&googleapi.ServerResponse{HTTPStatusCode: v.Code, Header: v.Header}, v.Unwrap())
	}

	valueOf := reflect.ValueOf(err)
	switch valueOf.Type().String() {
	case "http.http2GoAwayError", "http2.GoAwayError":
		return &http2.GoAwayError{
			LastStreamID: uint32(valueOf.FieldByName("LastStreamID").Uint()),
			ErrCode:      http2.ErrCode(valueOf.FieldByName("ErrCode").Uint()),
			DebugData:    valueOf.FieldByName("DebugData").String(),
		}
	default:
		if urlErr != nil {
			urlErr.Err = err
			err = urlErr
		}
		return err
	}
}
