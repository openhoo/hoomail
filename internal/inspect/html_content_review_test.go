package inspect

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// These names also appear in Chromium's accessibility tree for this markup.
func TestHTMLLinkNamesAndSourceOrder(t *testing.T) {
	body := `<a name="top"></a><a href="https://example.test/a" title="Help"></a>
<a href="https://example.test/b" aria-label="Fallback" aria-labelledby="action"></a><span id="action" hidden>Confirm</span>
<a href="https://example.test/c">Read <img alt="newsletter" src="cid:logo"> now</a>
<a href="https://example.test/d"><span style="opacity:0">Events</span></a>
<a href="https://example.test/e" hidden>Unseen</a>
<a href="https://example.test/f" aria-labelledby="contents">Fallback text</a><span id="contents"><span hidden>Secret</span> Home</span>
<a href="https://example.test/g"><span hidden>Missing</span></a>
<a href="https://example.test/h" aria-labelledby="brand"></a><img id="brand" alt="Brand">
<a href="https://example.test/i" aria-labelledby="legacy"></a><a name="legacy">Bookmark</a>`
	facts, causes, err := analyzeHTML(context.Background(), []byte(body), nil)
	if err != nil || len(causes) != 0 {
		t.Fatalf("causes=%v err=%v", causes, err)
	}
	if facts.anchorCount != 8 || facts.unnamedLinks != 2 {
		t.Errorf("checked=%d unnamed=%d", facts.anchorCount, facts.unnamedLinks)
	}
	var got []string
	for _, res := range facts.resources {
		got = append(got, res.url+"="+res.text)
	}
	want := []string{"https://example.test/a=Help", "https://example.test/b=Confirm", "https://example.test/c=Read newsletter now", "cid:logo=newsletter", "https://example.test/d=Events", "https://example.test/e=", "https://example.test/f=Home", "https://example.test/g=", "https://example.test/h=Brand", "https://example.test/i="}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestHTMLTrackingCandidateEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, attrs string
		tracking    bool
	}{
		{"decorative", `aria-hidden="true" width="480" height="80"`, false},
		{"faded", `style="opacity:0.5"`, false},
		{"transparent", `style="opacity: 0% !important"`, true},
		{"duplicate declarations", `style="display:none; display:block"`, false},
		{"important declaration", `style="display:none !important; display:block"`, true},
		{"tiny override", `width="600" style="width:1px !important"`, true},
		{"visible override", `width="1" style="width:120px"`, false},
		{"separator", `width="620" height="1"`, false},
		{"zero height", `width="620" height="0"`, true},
		{"negative dimension", `width="-1"`, false},
		{"embedded declaration text", `style="font-family:'display:none'; width:100%"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `<img alt="" src="//cdn.example.test/hero" ` + tc.attrs + `>`
			facts, causes, err := analyzeHTML(context.Background(), []byte(body), nil)
			if err != nil || len(causes) != 0 || facts.externalImages != 1 || (facts.trackingPixels != 0) != tc.tracking || facts.insecure != 0 {
				t.Errorf("external=%d tracking=%d insecure=%d causes=%v err=%v", facts.externalImages, facts.trackingPixels, facts.insecure, causes, err)
			}
		})
	}
}

func TestHTMLLinkBeforeNodeBudgetIsRetained(t *testing.T) {
	html := `<a href="http://example.test">First</a>` + strings.Repeat("<br>", MaxHTMLNodes)
	facts, causes, err := analyzeHTML(context.Background(), []byte(html), nil)
	if err != nil || !facts.truncated || len(causes) != 1 || len(facts.resources) != 1 || facts.resources[0].text != "First" || facts.insecure != 1 {
		t.Fatalf("truncated=%v causes=%v resources=%v insecure=%d err=%v", facts.truncated, causes, facts.resources, facts.insecure, err)
	}
}

func TestHTMLResponsiveImageSourcesAndInertTemplates(t *testing.T) {
	body := `<template><template><img src="https://template.example.test/unused"></template><a href="http://template.example.test">Unused</a></template>
<picture><source media="(min-width: 800px)" srcset="http://example.test/wide 1000w, //example.test/larger 1600w">
<img alt="" srcset="data:image/png;base64,YWJj 1x, https://example.test/v2,final.png 2x, https://example.test/invalid 2x 3x"></picture>
<video><source srcset="https://example.test/not-an-image"></video>
<img alt="" srcset="https://example.test/first, https://example.test/next 2x, https://example.test/bad unknown(a,b)">
<a href="https://example.test/live">Live</a>`
	facts, causes, err := analyzeHTML(context.Background(), []byte(body), nil)
	if err != nil || len(causes) != 0 || facts.externalImages != 5 || facts.insecure != 1 || facts.anchorCount != 1 || facts.imageCount != 2 || facts.trackingPixels != 0 {
		t.Fatalf("facts=%#v causes=%v err=%v", facts, causes, err)
	}
	var got []string
	for _, r := range facts.resources {
		got = append(got, r.url)
	}
	want := []string{"http://example.test/wide", "//example.test/larger", "data:image/png;base64,YWJj", "https://example.test/v2,final.png", "https://example.test/first", "https://example.test/next", "https://example.test/live"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resources=%q want=%q", got, want)
	}
}

func TestHTMLDocumentLanguageUsesBCP47Separators(t *testing.T) {
	for _, tc := range []struct {
		lang  string
		valid bool
	}{{"en_US", false}, {"en-US", true}, {"zh-Hant-TW", true}, {"x-example", true}} {
		facts, _, err := analyzeHTML(context.Background(), []byte(`<html lang="`+tc.lang+`"></html>`), nil)
		if err != nil || facts.langInvalid == tc.valid {
			t.Errorf("lang=%q invalid=%v err=%v", tc.lang, facts.langInvalid, err)
		}
	}
}

func TestHTMLPartialLinkNamesDistinguishReferenceFromConfirmedEmptyLink(t *testing.T) {
	body := `<a href="https://example.test/missing"></a><a href="https://example.test/later" aria-labelledby="label"></a>` + strings.Repeat("<br>", MaxHTMLNodes) + `<span id="label">Elsewhere</span>`
	facts, _, err := analyzeHTML(context.Background(), []byte(body), nil)
	if err != nil || !facts.truncated || facts.unnamedLinks != 1 {
		t.Fatalf("truncated=%v unnamed=%d err=%v", facts.truncated, facts.unnamedLinks, err)
	}
}

func TestHTMLNamedChildrenAndExplicitlyDisplayedLink(t *testing.T) {
	body := `<a href="https://example.test/track"><span aria-label="Track shipment"><i>Visual icon</i></span></a>
<a href="https://example.test/picture"><img alt="" title="Picture"></a>
<a hidden style="display:block" href="https://example.test/read">Read</a>`
	facts, _, err := analyzeHTML(context.Background(), []byte(body), nil)
	if err != nil || facts.anchorCount != 3 || facts.unnamedLinks != 0 {
		t.Fatalf("checked=%d unnamed=%d err=%v", facts.anchorCount, facts.unnamedLinks, err)
	}
	var got []string
	for _, r := range facts.resources {
		got = append(got, r.text)
	}
	if want := []string{"Track shipment", "Picture", "Read"}; !reflect.DeepEqual(got, want) {
		t.Errorf("names=%q want=%q", got, want)
	}
}
