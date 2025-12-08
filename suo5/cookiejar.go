package suo5

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sync"

	log "github.com/kataras/golog"
)

var _ http.CookieJar = (*SwitchableCookieJar)(nil)

type SwitchableCookieJar struct {
	http.CookieJar
	hintMap map[string]bool
	mu      sync.Mutex
	enable  bool
}

func NewSwitchableCookieJar(defaultEnable bool, hintKey []string) *SwitchableCookieJar {
	hintMap := make(map[string]bool)
	for _, key := range hintKey {
		hintMap[key] = true
	}
	defaultJar, _ := cookiejar.New(nil)
	return &SwitchableCookieJar{
		CookieJar: defaultJar,
		hintMap:   hintMap,
		enable:    defaultEnable,
	}
}

func (f *SwitchableCookieJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.enable {
		f.CookieJar.SetCookies(u, cookies)
		return
	}
	for _, cookie := range cookies {
		if _, ok := f.hintMap[cookie.Name]; ok {
			log.Infof("auto enable cookie jar for %s", cookie.Name)
			f.enable = true
			break
		}
	}
	if f.enable {
		log.Infof("setting cookie for %s", u.Host)
		f.CookieJar.SetCookies(u, cookies)
	}
}

func (f *SwitchableCookieJar) Cookies(u *url.URL) []*http.Cookie {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.enable {
		return f.CookieJar.Cookies(u)
	}
	return nil
}

func (f *SwitchableCookieJar) IsEnabled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.enable
}
