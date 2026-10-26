package geo

// ipwhoisResponse is the part of an https://ipwho.is/ response that we use.
// Failures come back as HTTP 200 with success set to false.
type ipwhoisResponse struct {
	Success     *bool      `json:"success"`
	Message     string     `json:"message"`
	IP          string     `json:"ip"`
	Country     string     `json:"country"`
	CountryCode string     `json:"country_code"`
	Region      string     `json:"region"`
	City        string     `json:"city"`
	Postal      flexString `json:"postal"`
}

func (r *ipwhoisResponse) toLocation() (Location, *ProviderError) {
	if r.Success != nil && !*r.Success {
		return Location{}, reportedFailure(KindRejected, r.Message)
	}
	return Location{
		ZipCode:     string(r.Postal),
		City:        r.City,
		Region:      r.Region,
		Country:     r.Country,
		CountryCode: r.CountryCode,
		IP:          r.IP,
	}, nil
}

// reportedFailure is a failure that a provider described in its response.
func reportedFailure(kind Kind, msg string) *ProviderError {
	if msg == "" {
		msg = "provider reported an error"
	}
	return failure(kind, "%s", msg)
}
