package geo

import "strings"

// ipapicoResponse is the part of an https://ipapi.co/json/ response that we
// use. Unknown values are null, and failures set error to true, sometimes
// with HTTP 200.
type ipapicoResponse struct {
	Error       bool       `json:"error"`
	Reason      string     `json:"reason"`
	Message     string     `json:"message"`
	IP          string     `json:"ip"`
	City        string     `json:"city"`
	Region      string     `json:"region"`
	Country     string     `json:"country"` // the ISO 3166-1 alpha-2 code
	CountryCode string     `json:"country_code"`
	CountryName string     `json:"country_name"`
	Postal      flexString `json:"postal"`
}

func (r *ipapicoResponse) toLocation() (Location, *ProviderError) {
	if r.Error {
		kind := KindRejected
		if strings.EqualFold(r.Reason, "RateLimited") {
			kind = KindRateLimited
		}
		msg := r.Reason
		if r.Message != "" {
			msg = strings.TrimPrefix(msg+": "+r.Message, ": ")
		}
		return Location{}, reportedFailure(kind, msg)
	}
	code := r.CountryCode
	if code == "" {
		code = r.Country
	}
	return Location{
		ZipCode:     string(r.Postal),
		City:        r.City,
		Region:      r.Region,
		Country:     r.CountryName,
		CountryCode: code,
		IP:          r.IP,
	}, nil
}
