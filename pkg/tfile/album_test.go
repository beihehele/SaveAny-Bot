package tfile

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gotd/td/tg"
)

func albumFile(id int, peer, group int64, caption string) TGFileMessage {
	return &tgFile{message: &tg.Message{ID: id, PeerID: &tg.PeerChannel{ChannelID: peer}, GroupedID: group, Message: caption}}
}

func TestAlbumDirectoriesShareCleanCaptionWithoutMergingGroups(t *testing.T) {
	files := []TGFileMessage{
		albumFile(12, 1, 10, ""), albumFile(11, 1, 10, "  春游 / 照片\n精选:*? "),
		albumFile(21, 1, 20, "春游 / 照片\n精选:*?"), albumFile(22, 1, 20, ""),
		albumFile(1, 2, 10, "另一组"), albumFile(2, 2, 10, ""),
		albumFile(30, 1, 0, "普通单文件"),
	}
	dirs := AlbumDirectories(files)
	for _, f := range files[:4] {
		if dirs[f.Message()] != "春游_照片_精选" {
			t.Fatalf("caption directory=%q", dirs[f.Message()])
		}
	}
	for _, f := range files[4:6] {
		if dirs[f.Message()] != "另一组" {
			t.Fatal("equal group ID in another chat inherited the wrong caption")
		}
	}
	if _, exists := dirs[files[6].Message()]; exists {
		t.Fatal("ordinary file was treated as an album")
	}
}

func TestAlbumDirectoryUsesEarliestUsableCaptionAndStableFallback(t *testing.T) {
	files := []TGFileMessage{albumFile(9, 1, 10, "later"), albumFile(2, 1, 10, "earlier")}
	if dirs := AlbumDirectories(files); dirs[files[0].Message()] != "earlier" {
		t.Fatal("arrival order changed the album directory")
	}
	empty := albumFile(1, 1, 20, ".. / :*")
	other := albumFile(1, 1, 21, "")
	first := AlbumDirectories([]TGFileMessage{empty, other})
	second := AlbumDirectories([]TGFileMessage{empty})
	if first[empty.Message()] != second[empty.Message()] || first[empty.Message()] == first[other.Message()] || !strings.HasPrefix(first[empty.Message()], "album_") {
		t.Fatal("empty captions need stable, distinct fallback directories")
	}
}

func TestAlbumIdentityIncludesPeerKind(t *testing.T) {
	files := []TGFileMessage{albumFile(1, 42, 99, "channel"), albumFile(1, 42, 99, "group"), albumFile(1, 42, 99, "user")}
	files[1].Message().PeerID = &tg.PeerChat{ChatID: 42}
	files[2].Message().PeerID = &tg.PeerUser{UserID: 42}
	dirs := AlbumDirectories(files)
	for _, file := range files {
		if dirs[file.Message()] != file.Message().Message {
			t.Fatal("peer kinds with equal numeric IDs shared a caption")
		}
	}
}

func TestCaptionDirectoryIsOneBoundedPortableComponent(t *testing.T) {
	for _, caption := range []string{"../../a\\b", "CON", "nul.txt", "LPT1", strings.Repeat("照片😀", 100)} {
		name := captionDirectory(caption)
		if !utf8.ValidString(name) || len(name) > 180 || utf8.RuneCountInString(name) > 64 || strings.ContainsAny(name, `/\:*?"<>|`) || strings.HasSuffix(name, ".") {
			t.Fatalf("unsafe name=%q", name)
		}
	}
	if captionDirectory("CON") != "_CON" || captionDirectory("nul.txt") != "_nul.txt" {
		t.Fatal("reserved names are not escaped")
	}
}

func TestPhotoFileSizeComesFromSelectedTelegramVariant(t *testing.T) {
	for _, variant := range []tg.PhotoSizeClass{
		&tg.PhotoSize{Type: "x", Size: 4},
		&tg.PhotoSizeProgressive{Type: "y", Sizes: []int{2, 4, 3}},
		&tg.PhotoCachedSize{Type: "m", Bytes: []byte("data")},
	} {
		media := &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 1, Sizes: []tg.PhotoSizeClass{variant}}}
		file, err := FromMedia(media, nil)
		if err != nil || file.Size() != 4 || file.Location().(*tg.InputPhotoFileLocation).ThumbSize != variant.GetType() {
			t.Fatalf("photo variant %T: file=%v error=%v", variant, file, err)
		}
	}
}

func TestNamelessDocumentFallbackPreservesCallerNamingOptions(t *testing.T) {
	media := &tg.MessageMediaDocument{Document: &tg.Document{ID: 7, Size: 4}}
	for _, scenario := range []struct {
		option TGFileOption
		want   string
	}{
		{want: "document_7"},
		{option: WithNameIfEmpty("caption-title.bin"), want: "caption-title.bin"},
		{option: WithName("custom.bin"), want: "custom.bin"},
	} {
		var options []TGFileOption
		if scenario.option != nil {
			options = append(options, scenario.option)
		}
		file, err := FromMedia(media, nil, options...)
		if err != nil || file.Name() != scenario.want {
			t.Fatalf("fallback name: file=%v want=%s error=%v", file, scenario.want, err)
		}
	}
}
