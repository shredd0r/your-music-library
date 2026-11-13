package tags

// Field is one metadata field ymlib writes. The string is its display name.
type Field string

const (
	FieldTitle        Field = "Title"
	FieldAlbum        Field = "Album"
	FieldArtist       Field = "Artist"
	FieldArtists      Field = "Artists"
	FieldAlbumArtist  Field = "Album Artist"
	FieldDate         Field = "Date"
	FieldTrackNumber  Field = "Track Number"
	FieldDiscNumber   Field = "Disc Number"
	FieldGenre        Field = "Genre"
	FieldPictureCover Field = "Picture Cover"
	// FieldPictureBack is never written; it appears in Diff when Format
	// removes an embedded back cover.
	FieldPictureBack Field = "Picture Back"
)

// Fields lists the fields ymlib writes, in display order. The set is
// fixed: there are no presets.
var Fields = []Field{
	FieldTitle, FieldAlbum, FieldArtist, FieldArtists, FieldAlbumArtist,
	FieldDate, FieldTrackNumber, FieldDiscNumber, FieldGenre, FieldPictureCover,
}
