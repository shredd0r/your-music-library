Q1 - I am thinking about 'https://lucida.to/' for download service. I want to use it with this integration. I can describe more details. It can help you before research. 
Q2 - TUI is prefer. But right now i start to doubt about this choose, because i want to show cover image to interface, but cli app - its just a text. I am not sure that i can render some image in TUI
Q3 - I tried run camoufox's execution file in golang playground and it works create. Maybe we can try to research lucida appies, and we can remove browser dependency
Q4 - variant 'B' looks exact how i think
Q5 - Whatever the source gives
Q6 - (1) feature
Q7 - right now, macOS/Linux. But need to add abstract layer, That helps to make Windows support

Q8 - Use this source https://monochrome.st
Q9 - All in Go with browser dependency
Q10 - MusicBrainz looks greate. But i want to add support looking metadatas from a few sources. For example, if one of source doesn't have metadata for some track, use another source. Next one is Discogs
Q11 - Yes, add both. Folder = Album Artist, not track Artist.
Q12 - Yes, a user-editable YAML config with built-ins. The preset applies on import, plus an explicit "re-tag" command (with a preview first) for existing files.
Q13 - I like configurable template. Also i thinking about singles. I searched how music libraries store singles - its just a album with one track. Need create feature, where user can choose: group singles to one album, order by date and change album tag to 'X singles' or save singles as album. And re-move-tag singles for one format

Q14 - I like B variant
Q15 - variant B
Q16 - I like feature "need review", keep it
Q17 - Yes, renumbering is fine. Track numbers are only for ordering in this mode anyway.
Q18 - Yes. Nothing half-downloaded ever enters the library folder.
Q19 - Yes. For sync feature, need to add flag 'sync without cover in album directory'

Q20 - Make Discogs primary source for metadata. If Discogs doesn't return or return error, cal MusicBrainz. Also, i thinking about last.fm and spotify for getting metadata for next stages.
Q21 - Yes. The "other edition" flag matters once remasters are involved.
Q22 - Yes. Is a deluxe edition's extra content what you'd want? If so, swap (5).
Q23 - Yes. prefer_remasters can also be set per artist, overriding the top-level value.
Q24 - Yes. Should playlists be made for every artist automatically, or only artists you pick? I recommend only the ones you pick.
Q25 - Yes.
Q26 - Need give user choose workflow for sync: full-mirror or sync only new and renamed/edited

Q27 - Yes. Mirroring a whole SD card root would delete the player's system files.
Q28 - For first stage - A realization. For next stages - button for enable background sync
Q29 - I think idea with "metadata preset" is bad. Remove it
Q30 - Yes, scanning and organizing are kept separate, and nothing moves without a preview.
Q31 - Yes. The cache also makes re-tagging and tests fast. Its good way save cache to SQLite? I thought about some in-memory storage
Q32 - Yes, with this screen set for v1.
Q33 - Yes
In addition, I though about all features and commands. I really like way claude code TUI way - write decired command to text block but i never realize feature like this, i dont know pitfalls of this feature

Q34 - Its better has two layers - in-memory, when app is active and SQLite - saving cache when exit from app for next run app. Also need to add clear cache after specific time, for example if cache saved 6 month ago we should delete it - the cache will not increase
Q35 - forget about it, maybe i brainstorm this feature for next stages.

## Sync update (2026-10-15)
Request: sync should handle devices, not only external storages. Some MP3 players have two storages (internal memory and an SD card). The user can group connected storages into one device. The workflow should work out how to split the library across the storages, or let the user handle it.
- Split unit: Artist (an artist is split by album only if it fits on no single storage).
- Placement in auto mode: fill storages in the user's order.
- Library doesn't fit: "App should calculate 'does albums fits?' befor start syncing" (fit check in the preview, before anything is copied).
- Only some storages connected: sync what's there.
