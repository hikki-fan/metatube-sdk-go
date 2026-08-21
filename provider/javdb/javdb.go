package javdb

import (
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/antchfx/htmlquery"
	"github.com/gocolly/colly/v2"
	"github.com/longbridgeapp/opencc"
	"golang.org/x/net/html"
	"golang.org/x/text/language"

	"github.com/metatube-community/metatube-sdk-go/common/fetch"
	"github.com/metatube-community/metatube-sdk-go/common/parser"
	"github.com/metatube-community/metatube-sdk-go/model"
	"github.com/metatube-community/metatube-sdk-go/provider"
	"github.com/metatube-community/metatube-sdk-go/provider/internal/scraper"
)

var (
	_ provider.MovieProvider = (*JavDB)(nil)
	_ provider.MovieSearcher = (*JavDB)(nil)
	_ provider.Fetcher       = (*JavDB)(nil)
)

const (
	Name = "JavDB"
	// Prefer JavDB when it returns an exact match. Fuzzy matches from other
	// providers are still ranked by the match score, so they remain fallbacks.
	Priority = 1010
)

const (
	baseURL   = "https://javdb.com/"
	movieURL  = "https://javdb.com/v/%s"
	searchURL = "https://javdb.com/search?f=all&q=%s"
)

var (
	scorePattern = regexp.MustCompile(`\d+(?:\.\d+)?`)

	// JavDB's Chinese locale currently returns Traditional Chinese genres even
	// when zh-CN is requested. Convert only genres to Simplified Chinese here;
	// titles, actor names and other Japanese metadata must remain untouched.
	genreConverter = func() *opencc.OpenCC {
		converter, err := opencc.New("tw2sp")
		if err != nil {
			panic(fmt.Sprintf("initialize JavDB genre converter: %v", err))
		}
		return converter
	}()
)

func simplifyGenre(genre string) string {
	simplified, err := genreConverter.Convert(genre)
	if err != nil {
		return genre
	}
	// OpenCC simplifies 企畫 to 企画; use the Mainland Chinese catalogue term.
	return strings.ReplaceAll(simplified, "企画", "企划")
}

type JavDB struct {
	*fetch.Fetcher
	*scraper.Scraper
}

func New() *JavDB {
	return &JavDB{
		Fetcher: fetch.Default(&fetch.Config{Referer: baseURL}),
		Scraper: scraper.NewDefaultScraper(
			Name, baseURL, Priority,
			language.Japanese,
			scraper.WithHeaders(map[string]string{
				"Accept-Language": "zh-CN,zh;q=0.9,ja;q=0.8,en;q=0.7",
				"Referer":         baseURL,
			}),
			scraper.WithCookies(baseURL, []*http.Cookie{
				{Name: "locale", Value: "zh-CN"},
			})),
	}
}

// JavDB uses a case-sensitive opaque ID in /v/<id> URLs.
func (jdb *JavDB) NormalizeMovieID(id string) string {
	return strings.TrimSpace(id)
}

func (jdb *JavDB) ParseMovieIDFromURL(rawURL string) (string, error) {
	homepage, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(homepage.Path, "/v/") {
		return "", provider.ErrInvalidURL
	}
	return jdb.NormalizeMovieID(path.Base(homepage.Path)), nil
}

func (jdb *JavDB) GetMovieInfoByID(id string) (*model.MovieInfo, error) {
	return jdb.GetMovieInfoByURL(fmt.Sprintf(movieURL, id))
}

func (jdb *JavDB) GetMovieInfoByURL(rawURL string) (info *model.MovieInfo, err error) {
	id, err := jdb.ParseMovieIDFromURL(rawURL)
	if err != nil {
		return nil, err
	}

	info = &model.MovieInfo{
		ID:            id,
		Provider:      jdb.Name(),
		Homepage:      rawURL,
		Actors:        []string{},
		PreviewImages: []string{},
		Genres:        []string{},
	}

	c := jdb.ClonedCollector()

	c.OnXML(`//div[contains(@class,"video-detail")]//h2[contains(@class,"title")]`, func(e *colly.XMLElement) {
		info.Number = strings.TrimSpace(e.ChildText(`.//strong[1]`))
		info.Title = strings.TrimSpace(e.ChildText(`.//strong[contains(@class,"current-title")]`))
	})

	c.OnXML(`//div[contains(@class,"column-video-cover")]`, func(e *colly.XMLElement) {
		if cover := extractImageSrc(e, `.//img[1]`); cover != "" {
			info.CoverURL = cover
			info.ThumbURL = cover
		}
	})

	c.OnXML(`//nav[contains(@class,"movie-panel-info")]/div[contains(@class,"panel-block")]`, func(e *colly.XMLElement) {
		label := strings.TrimSpace(e.ChildText(`.//strong[1]`))
		value := strings.TrimSpace(e.ChildText(`.//span[contains(@class,"value")]`))
		switch strings.TrimSuffix(label, ":") {
		case "番號", "番号", "ID":
			if value != "" {
				info.Number = strings.ReplaceAll(value, " ", "")
			}
		case "日期", "発売日", "Date":
			info.ReleaseDate = parser.ParseDate(value)
		case "時長", "时长", "収録時間", "Duration":
			info.Runtime = parser.ParseRuntime(value)
		case "導演", "导演", "監督", "Director":
			info.Director = strings.TrimSpace(e.ChildText(`.//span[contains(@class,"value")]//a[1]`))
		case "片商", "メーカー", "Maker":
			info.Maker = strings.TrimSpace(e.ChildText(`.//span[contains(@class,"value")]//a[1]`))
		case "發行", "发行", "発行", "Publisher":
			info.Label = strings.TrimSpace(e.ChildText(`.//span[contains(@class,"value")]//a[1]`))
		case "系列", "シリーズ", "Series":
			info.Series = strings.TrimSpace(e.ChildText(`.//span[contains(@class,"value")]//a[1]`))
		case "評分", "评分", "評価", "Rating":
			if score := scorePattern.FindString(value); score != "" {
				info.Score = parser.ParseScore(score)
			}
		case "類別", "类别", "カテゴリ", "Tags":
			for _, genre := range e.ChildTexts(`.//span[contains(@class,"value")]//a`) {
				if genre = strings.TrimSpace(genre); genre != "" {
					info.Genres = append(info.Genres, simplifyGenre(genre))
				}
			}
		case "演員", "演员", "出演者", "Actors":
			for _, aNode := range htmlquery.Find(e.DOM.(*html.Node), `.//span[contains(@class,"value")]//a`) {
				if isMaleActorNode(aNode) {
					continue
				}
				if actor := extractActorName(aNode); actor != "" {
					info.Actors = append(info.Actors, actor)
				}
			}
		}
	})

	c.OnXML(`//div[contains(@class,"preview-images")]//a[@data-fancybox]`, func(e *colly.XMLElement) {
		if imageURL := e.Request.AbsoluteURL(e.Attr("href")); imageURL != "" {
			info.PreviewImages = append(info.PreviewImages, imageURL)
		}
	})

	err = c.Visit(info.Homepage)
	return
}

func (jdb *JavDB) NormalizeMovieKeyword(keyword string) string {
	return strings.ToUpper(strings.TrimSpace(keyword))
}

func (jdb *JavDB) SearchMovie(keyword string) (results []*model.MovieSearchResult, err error) {
	c := jdb.ClonedCollector()
	c.OnXML(`//div[contains(@class,"movie-list")]//div[contains(@class,"item")]/a[contains(@class,"box")]`, func(e *colly.XMLElement) {
		homepage := e.Request.AbsoluteURL(e.Attr("href"))
		id, parseErr := jdb.ParseMovieIDFromURL(homepage)
		if parseErr != nil {
			return
		}
		cover := extractImageSrc(e, `.//div[contains(@class,"cover")]//img[1]`)
		results = append(results, &model.MovieSearchResult{
			ID:          id,
			Number:      strings.TrimSpace(e.ChildText(`.//div[contains(@class,"video-title")]/strong[1]`)),
			Title:       strings.TrimSpace(e.Attr("title")),
			Provider:    jdb.Name(),
			Homepage:    homepage,
			ThumbURL:    cover,
			CoverURL:    cover,
			ReleaseDate: parser.ParseDate(strings.TrimSpace(e.ChildText(`.//div[contains(@class,"meta")]`))),
		})
	})

	err = c.Visit(fmt.Sprintf(searchURL, url.QueryEscape(keyword)))
	return
}

func extractImageSrc(e *colly.XMLElement, xpath string) string {
	for _, attr := range []string{"data-src", "data-original", "data-lazy-src", "data-url", "src"} {
		if src := strings.TrimSpace(e.ChildAttr(xpath, attr)); src != "" {
			return e.Request.AbsoluteURL(src)
		}
	}
	return ""
}

func isMaleActorNode(n *html.Node) bool {
	for sib := n.NextSibling; sib != nil; sib = sib.NextSibling {
		if sib.Type == html.ElementNode {
			if sib.Data == "a" {
				break
			}
			if isMaleElement(sib) {
				return true
			}
			break
		}
	}
	if containsMaleElement(n) {
		return true
	}
	return false
}

func isMaleElement(n *html.Node) bool {
	var class string
	for _, attr := range n.Attr {
		if strings.EqualFold(attr.Key, "class") {
			class = attr.Val
			break
		}
	}
	classLower := strings.ToLower(class)
	if strings.Contains(classLower, "male") && !strings.Contains(classLower, "female") {
		return true
	}
	text := getNodeText(n)
	if strings.Contains(text, "♂") && !strings.Contains(text, "♀") {
		return true
	}
	return false
}

func isFemaleElement(n *html.Node) bool {
	var class string
	for _, attr := range n.Attr {
		if strings.EqualFold(attr.Key, "class") {
			class = attr.Val
			break
		}
	}
	classLower := strings.ToLower(class)
	if strings.Contains(classLower, "female") {
		return true
	}
	text := getNodeText(n)
	return strings.Contains(text, "♀")
}

func containsMaleElement(n *html.Node) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			if isMaleElement(c) || containsMaleElement(c) {
				return true
			}
		}
	}
	return false
}

func extractActorName(n *html.Node) string {
	var sb strings.Builder
	var collectText func(*html.Node)
	collectText = func(node *html.Node) {
		if node.Type == html.TextNode {
			sb.WriteString(node.Data)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && (isMaleElement(c) || isFemaleElement(c)) {
				continue
			}
			collectText(c)
		}
	}
	collectText(n)
	name := strings.TrimSpace(sb.String())
	name = strings.Trim(name, " \t\n\r,♀♂")
	return strings.TrimSpace(name)
}

func getNodeText(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			sb.WriteString(node.Data)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}

func init() {
	provider.Register(Name, New)
}
