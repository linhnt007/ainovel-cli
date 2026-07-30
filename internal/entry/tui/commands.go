package tui

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/host"
)

type slashCommandSpec struct {
	Name        string
	Aliases     []string
	Group       string
	Usage       string
	Description string
	AutoExecute bool
	Hidden      bool
	NeedsIdle   bool
	Run         func(m Model, args []string) (tea.Model, tea.Cmd)
}

type slashCommand struct {
	name string
	args []string
}

func parseSlashCommand(text string) (slashCommand, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return slashCommand{}, false
	}
	fields := strings.Fields(strings.TrimPrefix(text, "/"))
	if len(fields) == 0 {
		return slashCommand{}, false
	}
	return slashCommand{name: strings.ToLower(fields[0]), args: fields[1:]}, true
}

func (s slashCommandSpec) matches(name string) bool {
	if s.Name == name {
		return true
	}
	for _, alias := range s.Aliases {
		if strings.EqualFold(alias, name) {
			return true
		}
	}
	return false
}

func commandRegistryInstance() commandRegistry {
	return newCommandRegistry([]slashCommandSpec{
		{
			Name:        "help",
			Group:       "system",
			Usage:       "/help",
			Description: "Xem danh sách lệnh",
			AutoExecute: true,
			Run: func(m Model, _ []string) (tea.Model, tea.Cmd) {
				m.help = newHelpState(m.width, m.height)
				m.textarea.Blur()
				return m, nil
			},
		},
		{
			Name:        "new",
			Group:       "system",
			Usage:       "/new",
			Description: "Xóa dự án hiện tại và bắt đầu tạo tiểu thuyết mới",
			AutoExecute: true,
			Run: func(m Model, _ []string) (tea.Model, tea.Cmd) {
				if err := m.runtime.ResetAll(); err != nil {
					m.applyEvent(host.Event{
						Time: time.Now(), Category: "ERROR", Summary: "Tạo dự án mới thất bại: " + err.Error(), Level: "error",
					})
					m.refreshEventViewport()
					return m, nil
				}
				m.mode = modeNew
				m.startupMode = startupModeQuick
				m.resetOutputPanels()
				m.snapshot = m.runtime.Snapshot()
				m.textarea.Reset()
				m.textarea.Placeholder = placeholderForNewMode(startupModeQuick)
				m.textarea.Focus()
				m.cocreate = nil
				m.err = nil
				m.refreshEventViewport()
				m.refreshStreamViewport()
				m.refreshDetailViewport()
				m.refreshStateViewport()
				return m, nil
			},
		},
		{
			Name:        "model",
			Group:       "system",
			Usage:       "/model [role]",
			Description: "Chuyển đổi mô hình mặc định hoặc theo vai trò",
			AutoExecute: true,
			Run: func(m Model, args []string) (tea.Model, tea.Cmd) {
				roleHint := ""
				if len(args) > 0 {
					roleHint = args[0]
					if normalizeRoleKey(roleHint) == "" {
						m.applyEvent(host.Event{
							Time: time.Now(), Category: "ERROR", Summary: "Vai trò không xác định: " + roleHint, Level: "error",
						})
						m.refreshEventViewport()
						return m, nil
					}
				}
				m.modelSwitch = newModelSwitchState(m.runtime, roleHint)
				m.textarea.Blur()
				return m, nil
			},
		},
		{
			Name:        "diag",
			Group:       "analysis",
			Usage:       "/diag",
			Description: "Chẩn đoán tình trạng sáng tác tiểu thuyết",
			AutoExecute: true,
			Run: func(m Model, _ []string) (tea.Model, tea.Cmd) {
				m.reportSeq++
				m.report = newReportState(m.width, m.height, m.reportSeq, time.Now())
				m.textarea.Blur()
				return m, loadReport(m.runtime.Dir(), m.reportSeq)
			},
		},
		{
			Name:        "import",
			Group:       "writing",
			Usage:       "/import <path> [from=N]",
			Description: "Nhập truyện bên ngoài để tiếp tục viết",
			NeedsIdle:   true,
			Run: func(m Model, args []string) (tea.Model, tea.Cmd) {
				m.importSeq++
				state, listenCmd, err := startImport(m.runtime, m.importSeq, args, m.width, m.height)
				if err != nil {
					m.applyEvent(host.Event{
						Time: time.Now(), Category: "ERROR", Summary: "Khởi động nhập truyện thất bại: " + err.Error(), Level: "error",
					})
					m.refreshEventViewport()
					return m, nil
				}
				m.importer = state
				m.textarea.Blur()
				return m, listenCmd
			},
		},
		{
			Name:        "cocreate",
			Aliases:     []string{"plan"},
			Group:       "writing",
			Usage:       "/cocreate",
			Description: "Tạm dừng sáng tác, đồng sáng tác lên kế hoạch cho các giai đoạn tiếp theo",
			AutoExecute: true,
			Run: func(m Model, _ []string) (tea.Model, tea.Cmd) {
				if m.mode != modeRunning {
					m.applyEvent(host.Event{
						Time: time.Now(), Category: "ERROR", Summary: "Đồng sáng tác giai đoạn chỉ khả dụng khi đang sáng tác", Level: "error",
					})
					m.refreshEventViewport()
					return m, nil
				}
				if !m.runtime.PauseForCoCreate() {
					m.applyEvent(host.Event{
						Time: time.Now(), Category: "ERROR", Summary: "Không thể vào đồng sáng tác giai đoạn: toàn bộ tác phẩm đã hoàn thành hoặc đang trong quá trình đồng sáng tác", Level: "error",
					})
					m.refreshEventViewport()
					return m, nil
				}
				m.cocreate = newStageCoCreateState()
				m.resizeTextarea()
				m.textarea.Blur()
				return m, m.sendCoCreate()
			},
		},
		{
			Name:        "simulate",
			Group:       "writing",
			Usage:       "/simulate",
			Description: "Đọc ./simulate để tạo hoặc cập nhật tăng dần hồ sơ mô phỏng phong cách viết",
			NeedsIdle:   true,
			Run: func(m Model, args []string) (tea.Model, tea.Cmd) {
				m.simSeq++
				state, listenCmd, err := startSimulate(m.runtime, m.simSeq, args, m.width, m.height)
				if err != nil {
					m.applyEvent(host.Event{
						Time: time.Now(), Category: "ERROR", Summary: "Khởi động hồ sơ mô phỏng phong cách viết thất bại: " + err.Error(), Level: "error",
					})
					m.refreshEventViewport()
					return m, nil
				}
				m.simulator = state
				m.textarea.Blur()
				return m, listenCmd
			},
		},
		{
			Name:        "importsim",
			Group:       "writing",
			Usage:       "/importsim <profile.json>",
			Description: "Nhập hồ sơ mô phỏng phong cách có sẵn và hợp nhất theo dấu vân tay ngữ liệu",
			NeedsIdle:   true,
			Run: func(m Model, args []string) (tea.Model, tea.Cmd) {
				m.simSeq++
				state, listenCmd, err := startImportSimulation(m.runtime, m.simSeq, args, m.width, m.height)
				if err != nil {
					m.applyEvent(host.Event{
						Time: time.Now(), Category: "ERROR", Summary: "Nhập hồ sơ mô phỏng phong cách thất bại: " + err.Error(), Level: "error",
					})
					m.refreshEventViewport()
					return m, nil
				}
				m.simulator = state
				m.textarea.Blur()
				return m, listenCmd
			},
		},
		{
			Name:        "export",
			Group:       "writing",
			Usage:       "/export [path] [from=N] [to=M] [--overwrite]",
			Description: "Xuất truyện các chương đã hoàn thành sang TXT/EPUB",
			AutoExecute: true,
			Run: func(m Model, args []string) (tea.Model, tea.Cmd) {
				cmd, err := startExport(m.runtime, args)
				if err != nil {
					m.applyEvent(host.Event{
						Time: time.Now(), Category: "ERROR", Summary: "Khởi động xuất truyện thất bại: " + err.Error(), Level: "error",
					})
					m.refreshEventViewport()
					return m, nil
				}
				m.applyEvent(host.Event{
					Time: time.Now(), Category: "SYSTEM", Summary: "Đang xuất truyện...", Level: "info",
				})
				m.refreshEventViewport()
				return m, cmd
			},
		},
		{
			Name:        "gate",
			Group:       "writing",
			Usage:       "/gate ok [note]",
			Description: "Duyệt qua mốc kiểm duyệt để tiếp tục sáng tác",
			AutoExecute: false,
			Run: func(m Model, args []string) (tea.Model, tea.Cmd) {
				if len(args) == 0 || args[0] != "ok" {
					m.applyEvent(host.Event{
						Time: time.Now(), Category: "ERROR", Summary: "Sai cú pháp. Sử dụng: /gate ok [ghi chú]", Level: "error",
					})
					m.refreshEventViewport()
					return m, nil
				}

				progress, err := m.runtime.Store().Progress.Load()
				if err != nil || progress == nil || len(progress.CompletedChapters) == 0 {
					m.applyEvent(host.Event{
						Time: time.Now(), Category: "ERROR", Summary: "Không tìm thấy chương nào cần duyệt", Level: "error",
					})
					m.refreshEventViewport()
					return m, nil
				}
				lastCompleted := progress.CompletedChapters[len(progress.CompletedChapters)-1]

				note := ""
				if len(args) > 1 {
					note = strings.Join(args[1:], " ")
				}

				if err := m.runtime.Store().World.SaveHumanGateAck(lastCompleted, note); err != nil {
					m.applyEvent(host.Event{
						Time: time.Now(), Category: "ERROR", Summary: "Lưu xác nhận duyệt thất bại: " + err.Error(), Level: "error",
					})
					m.refreshEventViewport()
					return m, nil
				}

				// Fix 1E: Xóa frozen marker khi user ack gate.
				if err := m.runtime.Store().Progress.ClearHumanGateFreeze(); err != nil {
					slog.Warn("failed to clear human gate freeze", "err", err)
				}

				m.applyEvent(host.Event{
					Time: time.Now(), Category: "SYSTEM", Summary: fmt.Sprintf("Đã duyệt chương %d thành công.", lastCompleted), Level: "info",
				})

				if note != "" {
					chapter := progress.NextChapter()
					total := progress.TotalChapters
					_, err := m.runtime.Store().Directives.Add(domain.UserDirective{
						Text:          note,
						Chapter:       chapter,
						TotalChapters: total,
						CreatedAt:     time.Now().Format(time.RFC3339),
					})
					if err != nil {
						slog.Warn("Lưu chỉ thị từ human gate thất bại", "err", err)
					}
				}

				m.refreshEventViewport()

				// Cho chạy tiếp
				m.runtime.TriggerDispatch()
				return m, nil
			},
		},
		{
			Name:        "reset",
			Group:       "writing",
			Usage:       "/reset [chương]",
			Description: "Quay lui về chương chỉ định (mặc định: chương 0 — sau kiến trúc sư)",
			AutoExecute: true,
			Run: func(m Model, args []string) (tea.Model, tea.Cmd) {
				target := 0
				if len(args) > 0 {
					n, err := strconv.Atoi(args[0])
					if err != nil || n < 0 {
						m.applyEvent(host.Event{
							Time: time.Now(), Category: "ERROR", Summary: "Sai cú pháp. Sử dụng: /reset [số chương]", Level: "error",
						})
						m.refreshEventViewport()
						return m, nil
					}
					target = n
				}

				if err := m.runtime.ResetToChapter(target); err != nil {
					m.applyEvent(host.Event{
						Time: time.Now(), Category: "ERROR", Summary: "Quay lui thất bại: " + err.Error(), Level: "error",
					})
					m.refreshEventViewport()
					return m, nil
				}

				// Xóa sự kiện và stream TUI
				m.resetOutputPanels()
				m.mode = modeRunning
				enableMouse := m.enterRunning()
				m.resizeTextarea()
				m.textarea.Placeholder = defaultSteerPlaceholder()
				m.textarea.Reset()
				m.textarea.Focus()

				m.snapshot = m.runtime.Snapshot()
				m.refreshEventViewport()
				m.refreshStreamViewport()
				m.refreshDetailViewport()
				m.refreshStateViewport()
				return m, enableMouse
			},
		},
		{
			Name:        "resume",
			Group:       "writing",
			Usage:       "/resume",
			Description: "Tiếp tục quá trình sáng tác đang dở (khôi phục từ checkpoint)",
			AutoExecute: true,
			Run: func(m Model, _ []string) (tea.Model, tea.Cmd) {
				if m.snapshot.IsRunning {
					m.applyEvent(host.Event{
						Time: time.Now(), Category: "ERROR", Summary: "Đang trong quá trình sáng tác, không cần resume", Level: "error",
					})
					m.refreshEventViewport()
					return m, nil
				}
				m.resetOutputPanels()
				m.mode = modeRunning

				m.applyEvent(host.Event{
					Time: time.Now(), Category: "SYSTEM", Summary: "Đang khôi phục quá trình sáng tác...", Level: "info",
				})
				m.refreshEventViewport()
				return m, bootstrapRuntime(m.runtime)
			},
		},
	})
}

func commandSpecs() []slashCommandSpec {
	return commandRegistryInstance().Visible()
}

func (m Model) handleSlashCommand(cmd slashCommand) (tea.Model, tea.Cmd) {
	spec, ok := commandRegistryInstance().Find(cmd.name)
	if !ok {
		m.applyEvent(host.Event{
			Time: time.Now(), Category: "ERROR", Summary: "Lệnh không xác định: /" + cmd.name, Level: "error",
		})
		m.refreshEventViewport()
		return m, nil
	}
	if spec.NeedsIdle && m.snapshot.IsRunning {
		m.applyEvent(host.Event{
			Time: time.Now(), Category: "ERROR", Summary: "Lệnh chỉ có thể thực thi khi ở trạng thái rảnh: /" + spec.Name, Level: "error",
		})
		m.refreshEventViewport()
		return m, nil
	}
	return spec.Run(m, cmd.args)
}
