package tags

import (
	"os"
	"strings"

	"github.com/go-flac/flacpicture/v2"
	"github.com/go-flac/flacvorbis/v2"
	flac "github.com/go-flac/go-flac/v2"
)

// Vorbis comment keys, following MusicBrainz Picard where a convention exists.
const (
	vcTitle         = "TITLE"
	vcAlbum         = "ALBUM"
	vcArtist        = "ARTIST"
	vcArtists       = "ARTISTS"
	vcAlbumArtist   = "ALBUMARTIST"
	vcDate          = "DATE"
	vcTrackNumber   = "TRACKNUMBER"
	vcTrackTotal    = "TRACKTOTAL"
	vcDiscNumber    = "DISCNUMBER"
	vcDiscTotal     = "DISCTOTAL"
	vcGenre         = "GENRE"
	vcReleaseType   = "RELEASETYPE"
	vcISRC          = "ISRC"
	vcMBRecording   = "MUSICBRAINZ_TRACKID"
	vcMBRelease     = "MUSICBRAINZ_ALBUMID"
	vcDiscogs       = "DISCOGS_RELEASE_ID"
	vcSource        = "YMLIB_SOURCE"
	vcSourceTrackID = "YMLIB_SOURCE_TRACK_ID"
	vcOrigAlbum     = "YMLIB_ORIGINAL_ALBUM"
	vcOrigTrack     = "YMLIB_ORIGINAL_TRACK"
)

// aliases are other spellings read as the canonical key and removed on write.
var vcAliases = map[string]string{
	"ALBUM ARTIST": vcAlbumArtist,
	"YEAR":         vcDate,
	"TOTALTRACKS":  vcTrackTotal,
	"TOTALDISCS":   vcDiscTotal,
}

var vcManaged = map[string]bool{
	vcTitle: true, vcAlbum: true, vcArtist: true, vcArtists: true, vcAlbumArtist: true,
	vcDate: true, vcTrackNumber: true, vcTrackTotal: true, vcDiscNumber: true,
	vcDiscTotal: true, vcGenre: true, vcReleaseType: true, vcISRC: true,
	vcMBRecording: true, vcMBRelease: true, vcDiscogs: true, vcSource: true,
	vcSourceTrackID: true, vcOrigAlbum: true, vcOrigTrack: true,
}

func canonicalVCKey(k string) string {
	k = strings.ToUpper(k)
	if c, ok := vcAliases[k]; ok {
		return c
	}
	return k
}

type flacCodec struct{}

func (flacCodec) read(path string) (Track, error) {
	f, err := flac.ParseFile(path)
	if err != nil {
		return Track{}, err
	}
	defer f.Close()

	var t Track
	var other *Picture
	values := map[string][]string{}
	for _, b := range f.Meta {
		switch b.Type {
		case flac.VorbisComment:
			c, err := flacvorbis.ParseFromMetaDataBlock(*b)
			if err != nil {
				return Track{}, err
			}
			for _, kv := range c.Comments {
				k, v, ok := strings.Cut(kv, "=")
				if ok && v != "" {
					k = canonicalVCKey(k)
					values[k] = append(values[k], v)
				}
			}
		case flac.Picture:
			p, err := flacpicture.ParseFromMetaDataBlock(*b)
			if err != nil {
				return Track{}, err
			}
			pic := &Picture{MIME: p.MIME, Data: p.ImageData}
			switch p.PictureType {
			case flacpicture.PictureTypeFrontCover:
				t.Cover = pic
			case flacpicture.PictureTypeBackCover:
				t.Back = pic
			case flacpicture.PictureTypeOther:
				if other == nil {
					other = pic
				}
			}
		}
	}
	if t.Cover == nil {
		t.Cover = other // see coverFallbackIndex
	}

	first := func(k string) string {
		if v := values[k]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	t.Title = first(vcTitle)
	t.Album = first(vcAlbum)
	t.Artist = first(vcArtist)
	t.Artists = values[vcArtists]
	t.AlbumArtist = first(vcAlbumArtist)
	t.Date = first(vcDate)
	t.TrackNumber, t.TrackTotal = parseNumberPair(first(vcTrackNumber))
	t.DiscNumber, t.DiscTotal = parseNumberPair(first(vcDiscNumber))
	if n, _ := parseNumberPair(first(vcTrackTotal)); n != 0 {
		t.TrackTotal = n
	}
	if n, _ := parseNumberPair(first(vcDiscTotal)); n != 0 {
		t.DiscTotal = n
	}
	t.Genre = first(vcGenre)
	t.ReleaseType = first(vcReleaseType)
	t.IDs = Identity{
		Source:           first(vcSource),
		SourceTrackID:    first(vcSourceTrackID),
		ISRC:             first(vcISRC),
		MBRecordingID:    first(vcMBRecording),
		MBReleaseID:      first(vcMBRelease),
		DiscogsReleaseID: first(vcDiscogs),
	}
	t.OriginalAlbum = first(vcOrigAlbum)
	t.OriginalTrack, _ = parseNumberPair(first(vcOrigTrack))
	return t, nil
}

func (flacCodec) write(path string, t Track) error {
	f, err := flac.ParseFile(path)
	if err != nil {
		return err
	}

	var types []flacpicture.PictureType
	for _, b := range f.Meta {
		if b.Type == flac.Picture {
			p, err := flacpicture.ParseFromMetaDataBlock(*b)
			if err != nil {
				f.Close()
				return err
			}
			types = append(types, p.PictureType)
		}
	}
	fallback := coverFallbackIndex(types, flacpicture.PictureTypeFrontCover, flacpicture.PictureTypeOther)

	comment := flacvorbis.New()
	var meta []*flac.MetaDataBlock
	picIndex := -1
	for _, b := range f.Meta {
		switch b.Type {
		case flac.VorbisComment:
			old, err := flacvorbis.ParseFromMetaDataBlock(*b)
			if err != nil {
				f.Close()
				return err
			}
			comment.Vendor = old.Vendor
			for _, kv := range old.Comments {
				k, _, _ := strings.Cut(kv, "=")
				if !vcManaged[canonicalVCKey(k)] {
					comment.Comments = append(comment.Comments, kv)
				}
			}
		case flac.Picture:
			picIndex++
			typ := types[picIndex]
			if typ != flacpicture.PictureTypeFrontCover && typ != flacpicture.PictureTypeBackCover && picIndex != fallback {
				meta = append(meta, b)
			}
		default:
			meta = append(meta, b)
		}
	}

	add := func(k, v string) {
		if v != "" {
			comment.Comments = append(comment.Comments, k+"="+v)
		}
	}
	add(vcTitle, t.Title)
	add(vcAlbum, t.Album)
	add(vcArtist, t.Artist)
	for _, a := range t.Artists {
		add(vcArtists, a)
	}
	add(vcAlbumArtist, t.AlbumArtist)
	add(vcDate, t.Date)
	add(vcTrackNumber, padTrack(t.TrackNumber))
	add(vcTrackTotal, itoaOrEmpty(t.TrackTotal))
	add(vcDiscNumber, itoaOrEmpty(t.DiscNumber))
	add(vcDiscTotal, itoaOrEmpty(t.DiscTotal))
	add(vcGenre, t.Genre)
	add(vcReleaseType, t.ReleaseType)
	add(vcISRC, t.IDs.ISRC)
	add(vcMBRecording, t.IDs.MBRecordingID)
	add(vcMBRelease, t.IDs.MBReleaseID)
	add(vcDiscogs, t.IDs.DiscogsReleaseID)
	add(vcSource, t.IDs.Source)
	add(vcSourceTrackID, t.IDs.SourceTrackID)
	add(vcOrigAlbum, t.OriginalAlbum)
	add(vcOrigTrack, itoaOrEmpty(t.OriginalTrack))

	cb := comment.Marshal()
	meta = append(meta, &cb)
	for _, p := range []struct {
		pic *Picture
		typ flacpicture.PictureType
	}{{t.Cover, flacpicture.PictureTypeFrontCover}, {t.Back, flacpicture.PictureTypeBackCover}} {
		if p.pic == nil {
			continue
		}
		block, err := flacpicture.NewFromImageData(p.typ, "", p.pic.Data, p.pic.MIME)
		if err != nil {
			f.Close()
			return err
		}
		pb := block.Marshal()
		meta = append(meta, &pb)
	}
	f.Meta = meta

	// Write next to the original and rename, so a crash never leaves a
	// half-written file in the library.
	tmp := path + ".ymlib-tmp"
	err = f.Save(tmp)
	f.Close()
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if info, err := os.Stat(path); err == nil {
		os.Chmod(tmp, info.Mode())
	}
	if err := rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func (flacCodec) storedTrackNumber(path string) (string, error) {
	f, err := flac.ParseFile(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	for _, b := range f.Meta {
		if b.Type != flac.VorbisComment {
			continue
		}
		c, err := flacvorbis.ParseFromMetaDataBlock(*b)
		if err != nil {
			return "", err
		}
		for _, kv := range c.Comments {
			if k, v, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, vcTrackNumber) {
				return v, nil
			}
		}
	}
	return "", nil
}

func (flacCodec) canonicalTrackNumber(t Track) string { return padTrack(t.TrackNumber) }
