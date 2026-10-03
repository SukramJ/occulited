package system

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"

	"github.com/hobbyquaker/occulited/internal/addonimage"
	"github.com/hobbyquaker/occulited/internal/priv"
)

// An installed addon's icon and logo (openccu-lite task 100): the images its stored manifest
// declares (ui.icon, ui.icon_dark, ui.logo, ui.logo_dark - paths relative to the package root).
//
// The install keeps them (occulited task 11): taken out of the package archive at the declared
// path, root-owned beside the stored manifest as <id>.image-<kind>, so they are there however the
// addon's update_script lays out its files. Those copies are what the shell gets.
//
// An addon installed before, or whose manifest came from the catalogue rather than the package,
// has no copies; its images are read out of its tree as task 100 did, the path's first segment
// taken as the directory the installer copied to /usr/local/addons/<id>. The daemon reads the file
// itself where it may (a root addon's world-readable tree) and asks the helper where it may not (a
// confined addon's closed tree, openccu-lite B-252); either way the file is opened through os.Root
// on the tree, bounded, and answered only when its content is an image (internal/priv/addonimage.go).

// AddonImageInfix joins an addon id and an image kind to the name of the kept copy,
// AddonPolicyDir/<id>.image-<kind>.
const AddonImageInfix = ".image-"

// AddonImageKinds are the kinds of image an installed addon's stored manifest declares, in
// addonimage.Kinds' order; nil for an addon without a manifest or without images.
func (r Root) AddonImageKinds(id string) []string {
	m := r.ReadAddonManifest(id)
	if m == nil {
		return nil
	}
	return addonimage.DeclaredKinds(m.UI)
}

// AddonImage is one declared image of an installed addon, with the content type its content says.
// fs.ErrNotExist when the addon has no stored manifest, declares no such kind, or the file is not
// in its tree; another error when the file is there and cannot be served (a link out of the tree,
// too large, no image).
func (r Root) AddonImage(id, kind string) ([]byte, string, error) {
	if !addonIDRe.MatchString(id) {
		return nil, "", fmt.Errorf("%s: %w", id, fs.ErrNotExist)
	}
	manifestPath := r.join(AddonPolicyDir + "/" + id + AddonManifestSuffix)
	if b, err := r.keptAddonImage(id, kind); !errors.Is(err, fs.ErrNotExist) {
		if err != nil {
			return nil, "", err
		}
		ct, _ := addonimage.Sniff(b)
		return b, ct, nil
	}
	tree := r.join(priv.AddonHomeDir + id)
	b, err := priv.Local{}.ReadAddonImage(manifestPath, tree, kind)
	if errors.Is(err, fs.ErrPermission) {
		b, err = Priv.ReadAddonImage(manifestPath, tree, kind)
	}
	if err != nil {
		return nil, "", err
	}
	ct, ok := addonimage.Sniff(b)
	if !ok {
		return nil, "", priv.ErrImageNotImage
	}
	return b, ct, nil
}

// keptAddonImage is the copy the install kept of a declared image (occulited task 11).
// fs.ErrNotExist when there is none, or the stored manifest does not declare the kind (any more).
func (r Root) keptAddonImage(id, kind string) ([]byte, error) {
	m := r.ReadAddonManifest(id)
	if m == nil || addonimage.Declared(m.UI, kind) == "" {
		return nil, fmt.Errorf("%s %s: %w", id, kind, fs.ErrNotExist)
	}
	p := r.join(AddonPolicyDir + "/" + id + AddonImageInfix + kind)
	st, err := os.Lstat(p)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", id, kind, fs.ErrNotExist)
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: %w", p, priv.ErrImageNotRegular)
	}
	if st.Size() > addonimage.MaxSize {
		return nil, fmt.Errorf("%s: %w", p, priv.ErrImageTooLarge)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	if _, ok := addonimage.Sniff(b); !ok || len(b) > addonimage.MaxSize {
		return nil, fmt.Errorf("%s: %w", p, priv.ErrImageNotImage)
	}
	return b, nil
}

// keepAddonImages stores the images an install took out of the package beside the stored manifest
// and removes the copies of every kind it did not take: what is kept always belongs to the
// manifest stored with it. Answers the kinds kept.
func (r Root) keepAddonImages(id string, imgs map[string][]byte) ([]string, error) {
	var kept []string
	var errs []error
	for _, kind := range addonimage.Kinds {
		p := r.join(AddonPolicyDir + "/" + id + AddonImageInfix + kind)
		b, ok := imgs[kind]
		if !ok {
			if _, err := os.Lstat(p); err == nil {
				if err := remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
					errs = append(errs, err)
				}
			}
			continue
		}
		if err := writeFileAtomic(p, b, 0o644); err != nil {
			errs = append(errs, err)
			continue
		}
		kept = append(kept, kind)
	}
	return kept, errors.Join(errs...)
}

// removeAddonImages drops the kept copies: the stored manifest they belonged to is gone or came
// from elsewhere than the package.
func (r Root) removeAddonImages(id string) {
	if !addonIDRe.MatchString(id) {
		return
	}
	if _, err := r.keepAddonImages(id, nil); err != nil {
		slog.Warn("addon images: the kept copies could not be removed", "addon", id, "err", err)
	}
}
