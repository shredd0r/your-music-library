package main

import (
	"fmt"
	"os"

	"github.com/bogem/id3v2/v2"
	"github.com/dhowden/tag"
	flac "github.com/go-flac/go-flac/v2"
	"github.com/go-flac/flacpicture/v2"
	"github.com/go-flac/flacvorbis/v2"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func writeFLAC(path string, img []byte) {
	f, err := flac.ParseFile(path)
	must(err)
	// drop existing comments/pictures
	var kept []*flac.MetaDataBlock
	for _, b := range f.Meta {
		if b.Type != flac.VorbisComment && b.Type != flac.Picture {
			kept = append(kept, b)
		}
	}
	f.Meta = kept
	c := flacvorbis.New()
	for _, kv := range [][2]string{
		{"TITLE", "Bad"}, {"ALBUM", "Bad"}, {"ARTIST", "Michael Jackson"},
		{"ARTISTS", "Michael Jackson"}, {"ARTISTS", "Siedah Garrett"},
		{"ALBUMARTIST", "Michael Jackson"}, {"DATE", "1987-08-31"},
		{"TRACKNUMBER", "1"}, {"DISCNUMBER", "1"}, {"GENRE", "Pop"},
	} {
		must(c.Add(kv[0], kv[1]))
	}
	cb := c.Marshal()
	f.Meta = append(f.Meta, &cb)
	for _, pt := range []flacpicture.PictureType{flacpicture.PictureTypeFrontCover, flacpicture.PictureTypeBackCover} {
		p, err := flacpicture.NewFromImageData(pt, "", img, "image/jpeg")
		must(err)
		pb := p.Marshal()
		f.Meta = append(f.Meta, &pb)
	}
	must(f.Save(path))
}

func writeMP3(path string, img []byte) {
	t, err := id3v2.Open(path, id3v2.Options{Parse: true})
	must(err)
	defer t.Close()
	t.DeleteAllFrames()
	t.SetVersion(4)
	t.SetDefaultEncoding(id3v2.EncodingUTF8)
	t.SetTitle("Bad")
	t.SetAlbum("Bad")
	t.SetArtist("Michael Jackson")
	t.SetGenre("Pop")
	t.AddTextFrame("TDRC", id3v2.EncodingUTF8, "1987-08-31")
	t.AddTextFrame("TRCK", id3v2.EncodingUTF8, "1")
	t.AddTextFrame("TPOS", id3v2.EncodingUTF8, "1")
	t.AddTextFrame("TPE2", id3v2.EncodingUTF8, "Michael Jackson")
	// multi-value ARTISTS as TXXX (Picard convention), null-separated per v2.4
	t.AddUserDefinedTextFrame(id3v2.UserDefinedTextFrame{Encoding: id3v2.EncodingUTF8, Description: "ARTISTS", Value: "Michael Jackson\x00Siedah Garrett"})
	for _, pt := range []byte{id3v2.PTFrontCover, id3v2.PTBackCover} {
		t.AddAttachedPicture(id3v2.PictureFrame{Encoding: id3v2.EncodingUTF8, MimeType: "image/jpeg", PictureType: pt, Picture: img})
	}
	must(t.Save())
}

func readBack(path string) {
	f, err := os.Open(path)
	must(err)
	defer f.Close()
	m, err := tag.ReadFrom(f)
	must(err)
	tn, tt := m.Track()
	dn, _ := m.Disc()
	fmt.Printf("%s: title=%q artist=%q albumartist=%q album=%q year=%d track=%d/%d disc=%d genre=%q picture=%v\n",
		path, m.Title(), m.Artist(), m.AlbumArtist(), m.Album(), m.Year(), tn, tt, dn, m.Genre(), m.Picture() != nil)
	if path[len(path)-4:] == "flac" {
		ff, _ := flac.ParseFile(path)
		pics := 0
		for _, b := range ff.Meta {
			if b.Type == flac.VorbisComment {
				c, _ := flacvorbis.ParseFromMetaDataBlock(*b)
				v, _ := c.Get("ARTISTS")
				fmt.Println("  ARTISTS:", v)
			}
			if b.Type == flac.Picture {
				p, _ := flacpicture.ParseFromMetaDataBlock(*b)
				fmt.Println("  picture type:", p.PictureType)
				pics++
			}
		}
	} else {
		t, _ := id3v2.Open(path, id3v2.Options{Parse: true})
		for _, fr := range t.GetFrames("TXXX") {
			u := fr.(id3v2.UserDefinedTextFrame)
			fmt.Printf("  TXXX %s=%q\n", u.Description, u.Value)
		}
		for _, fr := range t.GetFrames("APIC") {
			fmt.Println("  APIC type:", fr.(id3v2.PictureFrame).PictureType)
		}
		t.Close()
	}
}

func main() {
	img, err := os.ReadFile("c.jpg")
	must(err)
	writeFLAC("s.flac", img)
	writeMP3("s.mp3", img)
	readBack("s.flac")
	readBack("s.mp3")
}
