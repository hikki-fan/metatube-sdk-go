package engine

import (
	"fmt"
	"image"
	"strings"

	"github.com/metatube-community/metatube-sdk-go/common/number"
	R "github.com/metatube-community/metatube-sdk-go/constant"
	"github.com/metatube-community/metatube-sdk-go/detector"
	"github.com/metatube-community/metatube-sdk-go/engine/providerid"
	"github.com/metatube-community/metatube-sdk-go/imageutil"
	"github.com/metatube-community/metatube-sdk-go/model"
	mt "github.com/metatube-community/metatube-sdk-go/provider"
)

// Default position constants for different kind of images.
const (
	defaultActorPrimaryImagePosition  = 0.5
	defaultMoviePrimaryImagePosition  = 1.0
	defaultMovieThumbImagePosition    = 0.5
	defaultMovieBackdropImagePosition = 0.0
	maxMovieImageFallbackAttempts     = 4
)

func (e *Engine) GetActorPrimaryImage(pid providerid.ProviderID) (image.Image, error) {
	info, err := e.GetActorInfoByProviderID(pid, true)
	if err != nil {
		return nil, err
	}
	if len(info.Images) == 0 {
		return nil, mt.ErrImageNotFound
	}
	return e.GetImageByURL(
		e.MustGetActorProviderByName(pid.Provider), info.Images[0],
		R.PrimaryImageRatio, defaultActorPrimaryImagePosition, false,
	)
}

func (e *Engine) GetMoviePrimaryImage(pid providerid.ProviderID, ratio, pos float64) (image.Image, error) {
	url, info, err := e.getPreferredMovieImageURLAndInfo(pid, true)
	if err != nil {
		return nil, err
	}
	if ratio < 0 /* default primary ratio */ {
		ratio = R.PrimaryImageRatio
	}
	var auto bool
	if pos < 0 /* manual position disabled */ {
		pos = defaultMoviePrimaryImagePosition
		auto = number.RequiresFaceDetection(info.Number)
	}
	provider := e.MustGetMovieProviderByName(pid.Provider)
	return e.getMovieImageWithFallback(provider, pid, url, ratio, pos, auto)
}

func (e *Engine) GetMovieThumbImage(pid providerid.ProviderID) (image.Image, error) {
	url, _, err := e.getPreferredMovieImageURLAndInfo(pid, false)
	if err != nil {
		return nil, err
	}
	provider := e.MustGetMovieProviderByName(pid.Provider)
	return e.getMovieImageWithFallback(provider, pid, url,
		R.ThumbImageRatio, defaultMovieThumbImagePosition, false)
}

func (e *Engine) GetMovieBackdropImage(pid providerid.ProviderID) (image.Image, error) {
	url, _, err := e.getPreferredMovieImageURLAndInfo(pid, false)
	if err != nil {
		return nil, err
	}
	provider := e.MustGetMovieProviderByName(pid.Provider)
	return e.getMovieImageWithFallback(provider, pid, url,
		R.BackdropImageRatio, defaultMovieBackdropImagePosition, false)
}

// GetMovieImageByURL fetches an explicit image URL and uses a provider's
// optional origin-page image fallback if fetching or decoding fails.
func (e *Engine) GetMovieImageByURL(pid providerid.ProviderID, url string, ratio, pos float64, auto bool) (image.Image, error) {
	provider, err := e.GetMovieProviderByName(pid.Provider)
	if err != nil {
		return nil, err
	}
	return e.getMovieImageWithFallback(provider, pid, url, ratio, pos, auto)
}

func (e *Engine) getMovieImageWithFallback(provider mt.MovieProvider, pid providerid.ProviderID, rawURL string, ratio, pos float64, auto bool) (image.Image, error) {
	img, imageErr := e.GetImageByURL(provider, rawURL, ratio, pos, auto)
	if imageErr == nil {
		return img, nil
	}
	fallbackProvider, ok := provider.(mt.MovieImageFallbackProvider)
	if !ok || !strings.EqualFold(provider.Name(), pid.Provider) {
		return nil, imageErr
	}
	id := provider.NormalizeMovieID(pid.ID)
	if id == "" {
		return nil, imageErr
	}
	urls, fallbackErr := fallbackProvider.GetMovieImageFallbackURLsByID(id)
	if fallbackErr != nil {
		return nil, fmt.Errorf("%w (movie image fallback failed: %v)", imageErr, fallbackErr)
	}

	seen := map[string]struct{}{strings.TrimSpace(rawURL): {}}
	attempts := 0
	for _, candidate := range urls {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		if attempts == maxMovieImageFallbackAttempts {
			break
		}
		attempts++
		if img, candidateErr := e.GetImageByURL(provider, candidate, ratio, pos, auto); candidateErr == nil {
			return img, nil
		} else {
			fallbackErr = candidateErr
		}
	}
	if fallbackErr == nil {
		fallbackErr = mt.ErrImageNotFound
	}
	return nil, fmt.Errorf("%w (movie image fallback failed: %v)", imageErr, fallbackErr)
}

func (e *Engine) GetImageByURL(provider mt.Provider, url string, ratio, pos float64, auto bool) (img image.Image, err error) {
	if img, err = e.getImageByURL(provider, url); err != nil {
		return
	}
	if auto {
		// only turn on advanced for movie providers.
		advancedMode := e.IsMovieProvider(provider.Name())
		axisR, found := detector.FindPrimaryFaceAxisRatio(img, ratio, advancedMode)
		if found {
			pos = axisR // override the default position with detected position.
		}
	}
	return imageutil.CropImagePosition(img, ratio, pos), nil
}

func (e *Engine) getImageByURL(provider mt.Provider, url string) (img image.Image, err error) {
	resp, err := e.Fetch(url, provider)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	img, _, err = imageutil.Decode(resp.Body)
	return
}

func (e *Engine) getPreferredMovieImageURLAndInfo(pid providerid.ProviderID, thumb bool) (url string, info *model.MovieInfo, err error) {
	info, err = e.GetMovieInfoByProviderID(pid, true)
	if err != nil {
		return
	}
	url = info.CoverURL
	if thumb && info.BigThumbURL != "" /* big thumb > cover */ {
		url = info.BigThumbURL
	} else if !thumb && info.BigCoverURL != "" /* big cover > cover */ {
		url = info.BigCoverURL
	}
	return
}
