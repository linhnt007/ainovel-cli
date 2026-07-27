package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
)

func main() {
	cfg, err := bootstrap.LoadConfig("")
	if err != nil {
		log.Fatalf("LoadConfig failed: %v", err)
	}

	ms, err := bootstrap.NewModelSet(cfg)
	if err != nil {
		log.Fatalf("NewModelSet failed: %v", err)
	}

	roles := []string{"default", "coordinator", "architect", "writer", "editor", "reviser"}
	fmt.Println("BẮT ĐẦU MODEL PROBE...")
	fmt.Println("--------------------------------------------------------------------------------")

	for _, role := range roles {
		var model agentcore.ChatModel
		if role == "default" {
			model = ms.Default
		} else {
			if _, ok := cfg.Roles[role]; !ok {
				continue
			}
			model = ms.ForRole(role)
		}

		modelName := bootstrap.ModelName(model)
		fmt.Printf("Role: %-15s | Model: %s\n", role, modelName)

		// Run Probe 1
		p1 := runProbe1(model)
		fmt.Printf("  Probe 1 (Tool-call): %s\n", p1)

		// Run Probe 2
		p2 := runProbe2(model)
		fmt.Printf("  Probe 2 (JSON heavy): %s\n", p2)

		// Run Probe 3
		p3 := runProbe3(model)
		fmt.Printf("  Probe 3 (Tiếng Việt): %s\n", p3)

		// Run Probe 4
		p4 := runProbe4(model)
		fmt.Printf("  Probe 4 (Context 12k): %s\n", p4)
		fmt.Println("--------------------------------------------------------------------------------")
	}
}

func runProbe1(model agentcore.ChatModel) string {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	echoTool := agentcore.ToolSpec{
		Name:        "echo",
		Description: "Echo back the text",
		Parameters: schema.Object(
			schema.Property("text", schema.String("Text to echo")).Required(),
		),
	}

	msgs := []agentcore.Message{
		agentcore.UserMsg("gọi tool echo với text='ping'"),
	}

	resp, err := model.Generate(ctx, msgs, []agentcore.ToolSpec{echoTool})
	if err != nil {
		return fmt.Sprintf("FAIL: error: %v", err)
	}

	var toolCalls []*agentcore.ToolCall
	if resp != nil {
		for _, b := range resp.Message.Content {
			if b.Type == agentcore.ContentToolCall && b.ToolCall != nil {
				toolCalls = append(toolCalls, b.ToolCall)
			}
		}
	}

	if len(toolCalls) != 1 {
		return "FAIL: no tool call or multiple tool calls"
	}

	tc := toolCalls[0]
	if tc.Name != "echo" {
		return fmt.Sprintf("FAIL: called wrong tool %q", tc.Name)
	}

	var args struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(tc.Args, &args); err != nil {
		return fmt.Sprintf("FAIL: cannot unmarshal args: %v", err)
	}

	if args.Text != "ping" {
		return fmt.Sprintf("FAIL: text is %q, want 'ping'", args.Text)
	}

	return "PASS"
}

func runProbe2(model agentcore.ChatModel) string {
	ctx := context.Background()

	issueSchema := schema.Object(
		schema.Property("type", schema.Enum("Chiều vấn đề", "consistency", "character", "pacing", "continuity", "foreshadow", "hook", "aesthetic")).Required(),
		schema.Property("severity", schema.Enum("Mức độ nghiêm trọng", "critical", "error", "warning")).Required(),
		schema.Property("description", schema.String("Mô tả vấn đề")).Required(),
		schema.Property("evidence", schema.String("Bằng chứng: đoạn trích nguyên văn, tình tiết cụ thể hoặc dữ liệu trạng thái")).Required(),
		schema.Property("suggestion", schema.String("Đề xuất chỉnh sửa")),
	)
	dimensionSchema := schema.Object(
		schema.Property("dimension", schema.Enum("Chiều", "consistency", "character", "pacing", "continuity", "foreshadow", "hook", "aesthetic")).Required(),
		schema.Property("score", schema.Int("Điểm số (0-100)")).Required(),
		schema.Property("verdict", schema.Enum("Kết luận chiều (có thể bỏ qua: hệ thống tự suy luận theo score, ≥80 pass / ≥60 warning / <60 fail)", "pass", "warning", "fail")),
		schema.Property("comment", schema.String("Kết luận ngắn gọn cho chiều này; mỗi chiều bắt buộc điền, aesthetic phải trích dẫn nguyên văn hoặc số liệu thống kê cụ thể")).Required(),
	)
	scoreTool := agentcore.ToolSpec{
		Name:        "score",
		Description: "Lưu kết quả rà soát",
		Parameters: schema.Object(
			schema.Property("chapter", schema.Int("Số chương")).Required(),
			schema.Property("scope", schema.Enum("Phạm vi", "chapter", "global", "arc")).Required(),
			schema.Property("dimensions", schema.Array("Điểm", dimensionSchema)).Required(),
			schema.Property("issues", schema.Array("Các vấn đề", issueSchema)).Required(),
			schema.Property("verdict", schema.Enum("Kết luận", "accept", "polish", "rewrite")).Required(),
			schema.Property("summary", schema.String("Tóm tắt")).Required(),
		),
	}

	passCount := 0
	for i := 0; i < 5; i++ {
		tctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		msgs := []agentcore.Message{
			agentcore.UserMsg("Hãy chấm điểm đoạn văn sau bằng công cụ score. Đoạn văn: 'Trời mưa tầm tã, Tiêu Viêm đi một mình trên phố vắng.'"),
		}
		resp, err := model.Generate(tctx, msgs, []agentcore.ToolSpec{scoreTool})
		cancel()

		var toolCalls []*agentcore.ToolCall
		if resp != nil {
			for _, b := range resp.Message.Content {
				if b.Type == agentcore.ContentToolCall && b.ToolCall != nil {
					toolCalls = append(toolCalls, b.ToolCall)
				}
			}
		}

		if err != nil || len(toolCalls) != 1 {
			continue
		}
		tc := toolCalls[0]
		var dummy map[string]any
		if err := json.Unmarshal(tc.Args, &dummy); err == nil {
			passCount++
		}
	}

	return fmt.Sprintf("%d/5 valid JSON", passCount)
}

func runProbe3(model agentcore.ChatModel) string {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	msgs := []agentcore.Message{
		agentcore.UserMsg("viết 150-200 từ tả cơn mưa, tiếng Việt"),
	}

	resp, err := model.Generate(ctx, msgs, nil)
	if err != nil {
		return fmt.Sprintf("FAIL: error: %v", err)
	}

	text := resp.Message.TextContent()
	cjkRegex := regexp.MustCompile(`[\p{Han}]`)
	if cjkRegex.MatchString(text) {
		return "FAIL: contains Han characters"
	}

	// Đếm tỷ lệ ký tự có dấu tiếng Việt
	accentedCount := 0
	totalRunes := utf8.RuneCountInString(text)
	if totalRunes == 0 {
		return "FAIL: empty response"
	}

	for _, r := range text {
		if (r >= 0x00C0 && r <= 0x017F) || (r >= 0x1E00 && r <= 0x1EFF) {
			accentedCount++
		}
	}

	ratio := float64(accentedCount) / float64(totalRunes)
	if ratio < 0.10 {
		return fmt.Sprintf("FAIL: low accent ratio %.2f%%", ratio*100)
	}

	return fmt.Sprintf("PASS (ratio %.2f%%)", ratio*100)
}

func runProbe4(model agentcore.ChatModel) string {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var sb strings.Builder
	sb.WriteString("Mã khóa là XUANVU-2026\n")
	for i := 0; i < 4000; i++ {
		sb.WriteString("Đây là văn bản đệm trung tính dùng để kiểm tra context window của mô hình ngôn ngữ lớn. ")
	}
	sb.WriteString("\nMã khóa ở đầu là gì?")

	msgs := []agentcore.Message{
		agentcore.UserMsg(sb.String()),
	}

	resp, err := model.Generate(ctx, msgs, nil)
	if err != nil {
		return fmt.Sprintf("FAIL: error: %v", err)
	}

	if strings.Contains(resp.Message.TextContent(), "XUANVU-2026") {
		return "PASS"
	}

	return "FAIL: cannot retrieve key from 12k context"
}
