package tags

import (
	"strings"

	"github.com/bogem/id3v2/v2"

	"your-music-library/internal/cleanup"
)

// TXXX descriptions, following MusicBrainz Picard where a convention exists.
const (
	txArtists       = "ARTISTS"
	txReleaseType   = "MusicBrainz Album Type"
	txMBRelease     = "MusicBrainz Album Id"
	txDiscogs       = "DISCOGS_RELEASE_ID"
	txSource        = "YMLIB_SOURCE"
	txSourceTrackID = "YMLIB_SOURCE_TRACK_ID"
	txOrigAlbum     = "YMLIB_ORIGINAL_ALBUM"
	txOrigTrack     = "YMLIB_ORIGINAL_TRACK"

	// Picard stores the MusicBrainz recording ID in a UFID frame.
	ufidMusicBrainz = "http://musicbrainz.org"
)

var txManaged = map[string]bool{
	strings.ToUpper(txArtists): true, strings.ToUpper(txReleaseType): true,
	strings.ToUpper(txMBRelease): true, strings.ToUpper(txDiscogs): true,
	strings.ToUpper(txSource): true, strings.ToUpper(txSourceTrackID): true,
	strings.ToUpper(txOrigAlbum): true, strings.ToUpper(txOrigTrack): true,
}

// Text frames fully owned by this package. TYER/TDAT are ID3v2.3 date
// frames, replaced by TDRC when written as v2.4.
var managedTextFrames = []string{"TIT2", "TALB", "TPE1", "TPE2", "TDRC", "TYER", "TDAT", "TRCK", "TPOS", "TCON", "TSRC"}

type mp3Codec struct{}

func (mp3Codec) read(path string) (_ Track, err error) {
	tag, err := id3v2.Open(path, id3v2.Options{Parse: true})
	if err != nil {
		return Track{}, err
	}
	defer cleanup.Close(&err, tag)

	text := func(id string) string { return strings.TrimRight(tag.GetTextFrame(id).Text, "\x00") }
	t := Track{
		Title:       text("TIT2"),
		Album:       text("TALB"),
		Artist:      text("TPE1"),
		AlbumArtist: text("TPE2"),
		Date:        text("TDRC"),
		Genre:       text("TCON"),
	}
	if t.Date == "" {
		t.Date = text("TYER")
	}
	t.TrackNumber, t.TrackTotal = parseNumberPair(text("TRCK"))
	t.DiscNumber, t.DiscTotal = parseNumberPair(text("TPOS"))
	t.IDs.ISRC = text("TSRC")

	for _, fr := range tag.GetFrames("TXXX") {
		u, ok := fr.(id3v2.UserDefinedTextFrame)
		if !ok {
			continue
		}
		v := strings.TrimRight(u.Value, "\x00")
		switch strings.ToUpper(u.Description) {
		case strings.ToUpper(txArtists):
			for substring := range strings.SplitSeq(v, "\x00") {
				if substring != "" {
					t.Artists = append(t.Artists, substring)
				}
			}
		case strings.ToUpper(txReleaseType):
			t.ReleaseType = v
		case strings.ToUpper(txMBRelease):
			t.IDs.MBReleaseID = v
		case strings.ToUpper(txDiscogs):
			t.IDs.DiscogsReleaseID = v
		case strings.ToUpper(txSource):
			t.IDs.Source = v
		case strings.ToUpper(txSourceTrackID):
			t.IDs.SourceTrackID = v
		case strings.ToUpper(txOrigAlbum):
			t.OriginalAlbum = v
		case strings.ToUpper(txOrigTrack):
			t.OriginalTrack, _ = parseNumberPair(v)
		}
	}
	for _, fr := range tag.GetFrames("UFID") {
		if u, ok := fr.(id3v2.UFIDFrame); ok && u.OwnerIdentifier == ufidMusicBrainz {
			t.IDs.MBRecordingID = string(u.Identifier)
		}
	}
	var other *Picture
	for _, fr := range tag.GetFrames("APIC") {
		p, ok := fr.(id3v2.PictureFrame)
		if !ok {
			continue
		}
		pic := &Picture{MIME: p.MimeType, Data: p.Picture}
		switch p.PictureType {
		case id3v2.PTFrontCover:
			t.Cover = pic
		case id3v2.PTBackCover:
			t.Back = pic
		case id3v2.PTOther:
			if other == nil {
				other = pic
			}
		}
	}
	if t.Cover == nil {
		t.Cover = other // see coverFallbackIndex
	}
	return t, nil
}

func (mp3Codec) write(path string, t Track) (err error) {
	tag, err := id3v2.Open(path, id3v2.Options{Parse: true})
	if err != nil {
		return err
	}
	defer cleanup.Close(&err, tag)

	// Keep unmanaged TXXX, UFID and APIC frames; drop managed ones.
	var keepTXXX []id3v2.UserDefinedTextFrame
	for _, fr := range tag.GetFrames("TXXX") {
		if u, ok := fr.(id3v2.UserDefinedTextFrame); ok && !txManaged[strings.ToUpper(u.Description)] {
			keepTXXX = append(keepTXXX, u)
		}
	}
	var keepUFID []id3v2.UFIDFrame
	for _, fr := range tag.GetFrames("UFID") {
		if u, ok := fr.(id3v2.UFIDFrame); ok && u.OwnerIdentifier != ufidMusicBrainz {
			keepUFID = append(keepUFID, u)
		}
	}
	var pics []id3v2.PictureFrame
	var types []byte
	for _, fr := range tag.GetFrames("APIC") {
		if p, ok := fr.(id3v2.PictureFrame); ok {
			pics = append(pics, p)
			types = append(types, p.PictureType)
		}
	}
	fallback := coverFallbackIndex(types, id3v2.PTFrontCover, id3v2.PTOther)
	var keepAPIC []id3v2.PictureFrame
	for i, p := range pics {
		if p.PictureType != id3v2.PTFrontCover && p.PictureType != id3v2.PTBackCover && i != fallback {
			keepAPIC = append(keepAPIC, p)
		}
	}
	for _, id := range append(managedTextFrames, "TXXX", "UFID", "APIC") {
		tag.DeleteFrames(id)
	}
	for _, u := range keepTXXX {
		tag.AddUserDefinedTextFrame(u)
	}
	for _, u := range keepUFID {
		tag.AddUFIDFrame(u)
	}
	for _, p := range keepAPIC {
		tag.AddAttachedPicture(p)
	}

	tag.SetVersion(4)
	tag.SetDefaultEncoding(id3v2.EncodingUTF8)
	addText := func(id, v string) {
		if v != "" {
			tag.AddTextFrame(id, id3v2.EncodingUTF8, v)
		}
	}
	addTXXX := func(desc, v string) {
		if v != "" {
			tag.AddUserDefinedTextFrame(id3v2.UserDefinedTextFrame{Encoding: id3v2.EncodingUTF8, Description: desc, Value: v})
		}
	}
	addText("TIT2", t.Title)
	addText("TALB", t.Album)
	addText("TPE1", t.Artist)
	addText("TPE2", t.AlbumArtist)
	addText("TDRC", t.Date)
	addText("TRCK", formatTrackPair(t.TrackNumber, t.TrackTotal))
	addText("TPOS", formatNumberPair(t.DiscNumber, t.DiscTotal))
	addText("TCON", t.Genre)
	addText("TSRC", t.IDs.ISRC)
	// ID3v2.4 separates multiple values with NUL (Picard convention for ARTISTS).
	addTXXX(txArtists, strings.Join(t.Artists, "\x00"))
	addTXXX(txReleaseType, t.ReleaseType)
	addTXXX(txMBRelease, t.IDs.MBReleaseID)
	addTXXX(txDiscogs, t.IDs.DiscogsReleaseID)
	addTXXX(txSource, t.IDs.Source)
	addTXXX(txSourceTrackID, t.IDs.SourceTrackID)
	addTXXX(txOrigAlbum, t.OriginalAlbum)
	addTXXX(txOrigTrack, itoaOrEmpty(t.OriginalTrack))
	if t.IDs.MBRecordingID != "" {
		tag.AddUFIDFrame(id3v2.UFIDFrame{OwnerIdentifier: ufidMusicBrainz, Identifier: []byte(t.IDs.MBRecordingID)})
	}
	for _, p := range []struct {
		pic *Picture
		typ byte
	}{{t.Cover, id3v2.PTFrontCover}, {t.Back, id3v2.PTBackCover}} {
		if p.pic != nil {
			tag.AddAttachedPicture(id3v2.PictureFrame{Encoding: id3v2.EncodingUTF8, MimeType: p.pic.MIME, PictureType: p.typ, Picture: p.pic.Data})
		}
	}
	// id3v2 writes to a temporary file and renames it over the original.
	return tag.Save()
}

func (mp3Codec) storedTrackNumber(path string) (_ string, err error) {
	tag, err := id3v2.Open(path, id3v2.Options{Parse: true, ParseFrames: []string{"TRCK"}})
	if err != nil {
		return "", err
	}
	defer cleanup.Close(&err, tag)
	return strings.TrimRight(tag.GetTextFrame("TRCK").Text, "\x00"), nil
}

func (mp3Codec) canonicalTrackNumber(t Track) string {
	return formatTrackPair(t.TrackNumber, t.TrackTotal)
}
