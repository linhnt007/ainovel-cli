package domain

// Novel là thông tin meta của tiểu thuyết.
type Novel struct {
	Name          string `json:"name"`
	TotalChapters int    `json:"total_chapters"`
}

// OutlineEntry là một mục trong đề cương, tương ứng với một chương.
type OutlineEntry struct {
	Chapter   int      `json:"chapter"`
	Title     string   `json:"title"`
	CoreEvent string   `json:"core_event"`
	Hook      string   `json:"hook"`
	Scenes    []string `json:"scenes"`
}

// Character là hồ sơ nhân vật.
type Character struct {
	Name        string   `json:"name"`
	Aliases     []string `json:"aliases,omitempty"` // bí danh/danh hiệu/biệt hiệu (ví dụ: "cậu bé phế vật", "anh Viêm")
	Role        string   `json:"role"`
	Description string   `json:"description"`
	Arc         string   `json:"arc"`
	Traits      []string `json:"traits"`
	Tier        string   `json:"tier,omitempty"` // core / important / secondary / decorative (mặc định là important)
}

// VolumeOutline là đề cương cấp tập (chế độ phân tầng cho truyện dài).
type VolumeOutline struct {
	Index int          `json:"index"`
	Title string       `json:"title"`
	Theme string       `json:"theme"` // xung đột/chủ đề cốt lõi của tập này
	Arcs  []ArcOutline `json:"arcs"`
}

// IsExpanded kiểm tra tập đã được mở rộng chưa (có cấu trúc cung truyện).
func (v *VolumeOutline) IsExpanded() bool { return len(v.Arcs) > 0 }

// StoryCompass là la bàn định hướng kết cục, thay thế danh sách tập khung cố định.
// Kiến trúc sư có thể cập nhật tại mỗi ranh giới tập, cho phép hướng truyện tiến hóa theo quá trình sáng tác.
type StoryCompass struct {
	EndingDirection string   `json:"ending_direction"`          // hướng kết cục (mô tả theo chủ đề)
	OpenThreads     []string `json:"open_threads,omitempty"`    // tuyến mở đang hoạt động (cần kết thúc trước khi kết cục)
	EstimatedScale  string   `json:"estimated_scale,omitempty"` // quy mô ước tính mơ hồ (ví dụ: "dự kiến 4-6 tập")
	LastUpdated     int      `json:"last_updated,omitempty"`    // số chương đã hoàn thành tại thời điểm cập nhật
}

// NarrativeContract là hợp đồng tường thuật của toàn tác phẩm: chuẩn ngôi kể / thì / nhân vật POV.
// Vì sao cần: không tầng nào trong hệ đặt chuẩn ngôi kể, model tự chọn ngẫu nhiên ở chương 1 rồi dễ trôi
// giữa sách mà biên tập không có mốc để so. Khai một lần tại foundation, tiêm vào working memory mỗi chương
// làm chuẩn cứng cho Người viết và mốc đối chiếu cho Biên tập viên.
//
// Optional — foundation cũ thiếu trường này thì mọi tầng bỏ qua êm (xem IsEmpty), hành vi cũ nguyên vẹn
// (không ràng buộc POV/thì).
type NarrativeContract struct {
	POV           string   `json:"pov,omitempty"`            // ngôi kể: "ngôi 1" / "ngôi 3 hạn tri" / "ngôi 3 toàn tri" / "đa POV"
	POVCharacters []string `json:"pov_characters,omitempty"` // nhân vật giữ POV (ngôi 1 hoặc ngôi 3 hạn tri)
	Tense         string   `json:"tense,omitempty"`          // thì trần thuật: "quá khứ" / "hiện tại"
	Notes         string   `json:"notes,omitempty"`          // quy tắc chuyển POV, ví dụ "đổi POV chỉ tại ranh giới chương; mỗi chương 1 POV"
}

// IsEmpty báo hợp đồng chưa được khai (foundation cũ, hoặc con trỏ nil). Dùng để mọi tầng bỏ qua êm.
func (n *NarrativeContract) IsEmpty() bool {
	return n == nil || (n.POV == "" && len(n.POVCharacters) == 0 && n.Tense == "" && n.Notes == "")
}

// ArcOutline là đề cương cấp cung truyện.
type ArcOutline struct {
	Index             int            `json:"index"` // số thứ tự cung truyện trong tập
	Title             string         `json:"title"`
	Goal              string         `json:"goal"`                         // mục tiêu của cung truyện (mở đầu-thắt nút-chuyển-kết)
	EstimatedChapters int            `json:"estimated_chapters,omitempty"` // số chương ước tính của cung khung (về 0 sau khi mở rộng)
	Chapters          []OutlineEntry `json:"chapters"`
}

// IsExpanded kiểm tra cung truyện đã được mở rộng chưa (có danh sách chương chi tiết).
func (a *ArcOutline) IsExpanded() bool { return len(a.Chapters) > 0 }

// TotalChapters tính tổng số chương đã lên kế hoạch hiện tại của đề cương phân tầng.
// Cung đã mở rộng được tính theo số chương thực tế, cung khung được tính theo EstimatedChapters.
// Progress.TotalChapters dùng hàm này để quyết định chiến lược ngữ cảnh cho truyện dài; các chương có thể viết thực sự vẫn lấy từ FlattenOutline.
func TotalChapters(volumes []VolumeOutline) int {
	n := 0
	for _, v := range volumes {
		for _, a := range v.Arcs {
			if a.IsExpanded() {
				n += len(a.Chapters)
			} else {
				n += a.EstimatedChapters
			}
		}
	}
	return n
}

// FlattenOutline trải phẳng đề cương phân tầng thành danh sách chương một chiều, giữ nguyên số chương toàn cục liên tục.
func FlattenOutline(volumes []VolumeOutline) []OutlineEntry {
	var result []OutlineEntry
	ch := 1
	for _, v := range volumes {
		for _, a := range v.Arcs {
			for _, e := range a.Chapters {
				e.Chapter = ch
				result = append(result, e)
				ch++
			}
		}
	}
	return result
}

// WorldRule là một mục quy tắc thế giới quan.
type WorldRule struct {
	Category string `json:"category"` // magic / technology / geography / society / other
	Rule     string `json:"rule"`     // mô tả quy tắc
	Boundary string `json:"boundary"` // ranh giới không được vi phạm
}
