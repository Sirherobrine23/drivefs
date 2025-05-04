package drivefs

import (
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"reflect"

	"github.com/googleapis/gax-go/v2/apierror"
	"golang.org/x/net/http2"
	"google.golang.org/api/googleapi"
)

var (
	ErrHttp2 = errors.New("http2 error")
	ErrQuota = errors.New("api quota limit")
)

func ProcessErr(res *http.Response, err error) error {
	if res != nil {
		switch res.StatusCode {
		case http.StatusNotFound:
			return fs.ErrNotExist
		case http.StatusTooManyRequests:
			return fs.ErrPermission
		}
	}

	urlErr := (*url.Error)(nil)
	switch v := err.(type) {
	case *http2.GoAwayError:
		return ErrHttp2
	case *url.Error:
		urlErr = v
		err = v.Err
	case *apierror.APIError:
		details := v.Details()
		if details.QuotaFailure != nil {
			return ErrQuota
		}

		switch v.HTTPCode() {
		case http.StatusNotFound:
			return fs.ErrNotExist
		case http.StatusTooManyRequests:
			return ErrQuota
		}

		err = v.Unwrap()
	case *googleapi.Error:
		switch v.Code {
		case http.StatusNotFound:
			return fs.ErrNotExist
		case http.StatusTooManyRequests:
			return ErrQuota
		}
		err = v.Unwrap()
	}

	valueOf := reflect.ValueOf(err)
	switch valueOf.Type().String() {
	case "http.http2GoAwayError", "http2.GoAwayError":
		return &http2.GoAwayError{
			DebugData:    valueOf.FieldByName("DebugData").String(),
			ErrCode:      http2.ErrCode(uint32(valueOf.FieldByName("ErrCode").Uint())),
			LastStreamID: uint32(valueOf.FieldByName("LastStreamID").Uint()),
		}
	default:
		if urlErr != nil {
			urlErr.Err = err
			err = urlErr
		}
		return err
	}
}
