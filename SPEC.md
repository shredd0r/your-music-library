## Idea of project
Download music from web resources and manage downloaded music or users library.
User can manage his library for fixing music metadata, search some music file and read already saved metadata and cover list.

## Features:

### Choosing metadata fields for downloading and manage.
User can choose which metadata fields he want to save for future music files in his library. Fields:
- Music title 'Title'
- Album name 'Album'
- Main artist 'Artist'
- All artists, who works on title 'Artists'
- Release date, month/day/year 'Date'
- Track number in Album 'Track Number'
- Front cover for title 'Picture Cover'
- Back cover for title 'Picture Back' (if exist)
- Genre 'Genre'

For better user experience, tool has to have support preset metadata fields, for example:
- Preset for MP3 players, with fields 
  - Title
  - Album
  - Artist
  - Date
  - Track Number
  - Picture Cover
  - Genre

### Pick track/album from web services for downloading. 
User choose what exactly he want to download (type of subject) and write title of subject (track name or album name). App should show list of matched subjects namely:
- cover (if it can be realize in TUI)
- title name
- artist
- lenght
- quality
- explicit
When user pick title for download, title puts in queue for downloading. Downloading occurs by track.
When user looking for download some track, found titles should have flag "already downloaded" for avoiding duplicates
Downloaded title should store by this path: /user-choosen-library-path/artist-name/album-name/track-name

### Create playlist with all artist discography which user has.
User can create playlist "Its 'artist-name'". It is playlist where placed all titles and track numbers.
Information for ordering takes from metadata of titles.
For Example:

Downloaded titles:
- Michael Jackson 
  - Bad (05/31/1987)
    - 01 - Bad
    - 03 - Speed Demon
  - Off the Wall (05/10/1979)
    - 01 - Don't Stop 'Til You Get Enough
    - 02 - Rock with You

Result:
- 01 - Don't Stop 'Til You Get Enough
- 02 - Rock with You
- 01 - Bad
- 03 - Speed Demon

### User can wrote list of titles for download
User wrote list of titles for download grouped by albums or singles. Schema of file:
```yaml
- artist-1:
  albums:
  - album-1
  - album-2
  titles:
  - single-1
- artist-2:...
```
When user choose this file for download, app should find one by one all albums/titles by artist and add it to downloading queue. App should choosen album/title download by specific algorithm:
If web service has a few similar albums/titles - choose with greatest quality and explicity edition.
User can enable flag 'Prefer Remasters'. If user enable this flag, while searching, app will add remaster album instead of original. 

### Sync library to external data storage (external SSD, SD-cards, MP3 players, smartphone etc).
User choose connected external storages (hdd/player etc) app in background will be check current user library hash to library hash from external storages. If some tracks missed - app copy missed tracks to external storage. Process should be show on some corner.

# Notes:

https://github.com/daijro/camoufox/tree/main/pythonlib#camoufox-python-interface
this will be for install 'camoufox'. This browser should help for avoid bot checking for some web services for download music
