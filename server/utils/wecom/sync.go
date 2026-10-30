package wecom

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// 本文件是通讯录同步的「纯函数」部分：输入「本地现状 + 企微快照」，输出「要做的变更」。
// 不读写数据库、不发网络请求，因此可以脱离真实企业微信环境单独验证（见 sync_test.go）。
//
// 之所以把规划单独抽出来，是因为真实同步里有几个不可逆的判断（停用账号、覆盖姓名、
// 改上级），先算清楚再落库，比边拉边写安全得多。

// LocalDept 本地部门现状。
type LocalDept struct {
	ID       uint
	Name     string
	ParentID uint
	// ExternalID 对应的企微部门 ID；空字符串表示尚未关联（首次同步/手工建的部门）。
	ExternalID string
}

// LocalEmployee 本地员工现状。
type LocalEmployee struct {
	ID          uint
	Name        string
	Email       string
	WeComUserID string // 空字符串表示尚未关联企微成员
	Position    string
	DeptID      uint
	ManagerID   *uint
	Role        string
	IsActive    bool
}

// LocalState 本地现状快照。
type LocalState struct {
	Depts     []LocalDept
	Employees []LocalEmployee
}

// DeptCreate 要新建的部门。
type DeptCreate struct {
	// ExternalID 企微部门 ID。
	ExternalID string
	// ParentExternalID 上级部门的企微 ID；空字符串表示顶级部门。
	ParentExternalID string
	Name             string
}

// DeptUpdate 要更新的部门。
type DeptUpdate struct {
	LocalID uint
	// ExternalID 企微部门 ID。落地时如果本地还没关联，就写入它。
	ExternalID string
	// Name 非 nil 表示部门改名。
	Name *string
	// ParentExternalID 非 nil 表示上级变化。指向空字符串表示改为顶级部门。
	ParentExternalID *string
}

// EmployeeCreate 要新建的员工。
type EmployeeCreate struct {
	WeComUserID string
	Name        string
	Email       string
	Position    string
	// DeptExternalID 所属部门的企微 ID；落地时由调用方换成本地部门 ID。
	DeptExternalID string
	// ManagerWeComUserID 上级的企微 UserID；空字符串表示没推断出上级。
	// 该上级可能也在本次新建列表里，所以调用方应在全部创建完成后再统一回填。
	ManagerWeComUserID string
	Role               string
}

// EmployeeUpdate 要更新的员工。字段为指针，nil 表示不修改。
type EmployeeUpdate struct {
	LocalID uint
	// WeComUserID 企微成员 UserID。落地时如果本地还没关联，就写入它。
	WeComUserID string
	Name        *string
	Email       *string
	Position    *string
	// DeptExternalID 非 nil 表示所属部门变化。
	DeptExternalID *string
	// ManagerSet 为 true 时，才用 ManagerWeComUserID 回填上级（空字符串表示清空）。
	// 规划器只在本地没有上级时设置它，避免覆盖 HR 手工调整。
	ManagerSet         bool
	ManagerWeComUserID string
	// Role 非 nil 表示角色变化（只会升为 manager，不会降级）。
	Role *string
}

// Plan 一次同步的完整变更集。
type Plan struct {
	// DeptCreates 已按层级排序（父部门在前），可直接顺序落地。
	DeptCreates []DeptCreate
	DeptUpdates []DeptUpdate
	// EmployeeCreates 中的 ManagerWeComUserID 需要等全部员工创建后再统一回填。
	EmployeeCreates []EmployeeCreate
	EmployeeUpdates []EmployeeUpdate
	// DeactivatedLocalIDs 需要停用的本地员工（企微已停用或已从通讯录移除）。
	DeactivatedLocalIDs []uint
	// Warnings 需要 HR 人工处理的情况（缺邮箱、多个负责人、上级不一致等）。
	Warnings []string
}

// Empty 表示没有任何变更。
func (p Plan) Empty() bool {
	return len(p.DeptCreates) == 0 &&
		len(p.DeptUpdates) == 0 &&
		len(p.EmployeeCreates) == 0 &&
		len(p.EmployeeUpdates) == 0 &&
		len(p.DeactivatedLocalIDs) == 0
}

// Summary 便于写日志的一行摘要。
func (p Plan) Summary() string {
	return fmt.Sprintf(
		"部门 新建 %d / 更新 %d；员工 新建 %d / 更新 %d / 停用 %d；待人工处理 %d",
		len(p.DeptCreates), len(p.DeptUpdates),
		len(p.EmployeeCreates), len(p.EmployeeUpdates), len(p.DeactivatedLocalIDs),
		len(p.Warnings),
	)
}

// SyncOptions 同步策略。
type SyncOptions struct {
	// PromoteDeptLeaders 把部门负责人自动提升为 manager 角色（只升不降）。
	// HR 手工设置的 hr 角色不会被覆盖。
	PromoteDeptLeaders bool
	// OverwriteManager 用企微推断的上级覆盖本地已有的上级。
	// 默认 false：企微只在本地没有上级时补位，HR 的手工调整不会被冲掉。
	OverwriteManager bool
}

// BuildPlan 计算同步计划。
//
// 规则（按优先级）：
//
//  1. 以企微为准同步部门和员工的基本信息（名称、职位、所属部门、启停状态）。
//  2. 员工按「企微 UserID → 邮箱」的顺序匹配本地账号，邮箱按小写去重；
//     Email 是本地唯一且非空字段，因此没有邮箱的成员无法建号，只报警告。
//  3. 上级推断：从成员所在部门开始向上找，取第一个「唯一负责人」作为上级；
//     该部门有多个负责人时视为无法确定，交给 HR；整条链都没有负责人时，
//     仍然正常建号（账号可登录），只是在 HR 补上直属上级之前无法创建考核。
//  4. 已存在员工永远不会被自动停用或清空上级。
//  5. 通讯录为空时直接返回警告，避免把全部账号误停用。
func BuildPlan(local LocalState, users []User, depts []Dept, opts SyncOptions) Plan {
	var plan Plan

	if len(users) == 0 {
		plan.Warnings = append(plan.Warnings, "企业微信未返回任何成员，已跳过同步（避免误停用全部账号）")
		return plan
	}

	// ------------------------------------------------------------------
	// 部门
	// ------------------------------------------------------------------

	wecomDeptByID := make(map[int64]Dept, len(depts))
	for _, d := range depts {
		// 企微根部门（ID=1）代表公司本身，不落地为本地部门，其子部门作为顶级部门。
		if d.ID == rootDeptID {
			continue
		}
		wecomDeptByID[d.ID] = d
	}

	childrenOf := make(map[int64][]Dept, len(wecomDeptByID))
	for _, d := range wecomDeptByID {
		childrenOf[d.ParentID] = append(childrenOf[d.ParentID], d)
	}
	for parentID := range childrenOf {
		siblings := childrenOf[parentID]
		sort.Slice(siblings, func(i, j int) bool {
			if siblings[i].Order != siblings[j].Order {
				return siblings[i].Order < siblings[j].Order
			}
			return siblings[i].ID < siblings[j].ID
		})
	}

	localDeptByID := make(map[uint]*LocalDept, len(local.Depts))
	localDeptByExternal := make(map[string]*LocalDept, len(local.Depts))
	unlinkedByName := make(map[string][]*LocalDept)
	for i := range local.Depts {
		dept := &local.Depts[i]
		localDeptByID[dept.ID] = dept
		if dept.ExternalID != "" {
			localDeptByExternal[dept.ExternalID] = dept
			continue
		}
		if dept.Name != "" {
			unlinkedByName[dept.Name] = append(unlinkedByName[dept.Name], dept)
		}
	}

	// currentParentExternal 返回本地部门当前上级对应的企微 ID（顶级部门为空）。
	currentParentExternal := func(dept *LocalDept) string {
		if dept.ParentID == 0 {
			return ""
		}
		parent, ok := localDeptByID[dept.ParentID]
		if !ok {
			return ""
		}
		return parent.ExternalID
	}

	// 广度优先遍历，保证父部门先于子部门处理（新建时才能拿到父部门）。
	// claimedDepts 防止两个同名企微部门在同一轮里匹配到同一个本地部门。
	claimedDepts := make(map[uint]bool, len(local.Depts))
	queue := []int64{rootDeptID}
	visited := map[int64]bool{rootDeptID: true}

	for len(queue) > 0 {
		parentWeComID := queue[0]
		queue = queue[1:]

		for _, wecomDept := range childrenOf[parentWeComID] {
			if visited[wecomDept.ID] {
				continue
			}
			visited[wecomDept.ID] = true
			queue = append(queue, wecomDept.ID)

			externalID := strconv.FormatInt(wecomDept.ID, 10)
			parentExternalID := ""
			if wecomDept.ParentID != rootDeptID {
				parentExternalID = strconv.FormatInt(wecomDept.ParentID, 10)
			}

			matched := localDeptByExternal[externalID]
			if matched == nil {
				// 首次同步：本地还没有 ExternalID，按名称匹配「尚未关联」的部门。
				// 只在名称唯一时匹配，避免把同名的两个部门关联错。
				var candidates []*LocalDept
				for _, candidate := range unlinkedByName[wecomDept.Name] {
					if !claimedDepts[candidate.ID] {
						candidates = append(candidates, candidate)
					}
				}
				if len(candidates) == 1 {
					matched = candidates[0]
					claimedDepts[matched.ID] = true
				}
			}

			if matched == nil {
				plan.DeptCreates = append(plan.DeptCreates, DeptCreate{
					ExternalID:       externalID,
					ParentExternalID: parentExternalID,
					Name:             wecomDept.Name,
				})
				continue
			}

			update := DeptUpdate{LocalID: matched.ID, ExternalID: externalID}

			if matched.Name != wecomDept.Name && wecomDept.Name != "" {
				name := wecomDept.Name
				update.Name = &name
			}
			if currentParentExternal(matched) != parentExternalID {
				parent := parentExternalID
				update.ParentExternalID = &parent
			}
			if update.Name != nil || update.ParentExternalID != nil || matched.ExternalID == "" {
				plan.DeptUpdates = append(plan.DeptUpdates, update)
			}
		}
	}

	// 部门新建顺序：按层级从浅到深，父部门先建。
	sort.SliceStable(plan.DeptCreates, func(i, j int) bool {
		return deptCreateDepth(plan.DeptCreates[i], wecomDeptByID) < deptCreateDepth(plan.DeptCreates[j], wecomDeptByID)
	})

	// ------------------------------------------------------------------
	// 员工
	// ------------------------------------------------------------------

	localEmpByID := make(map[uint]*LocalEmployee, len(local.Employees))
	localEmpByWeCom := make(map[string]*LocalEmployee, len(local.Employees))
	localEmpByEmail := make(map[string][]*LocalEmployee, len(local.Employees))
	for i := range local.Employees {
		emp := &local.Employees[i]
		localEmpByID[emp.ID] = emp
		if emp.WeComUserID != "" {
			localEmpByWeCom[emp.WeComUserID] = emp
		}
		if email := normalizeEmail(emp.Email); email != "" {
			localEmpByEmail[email] = append(localEmpByEmail[email], emp)
		}
	}

	// 部门负责人：企微部门 ID → 负责人 UserID 列表
	leaders := make(map[int64][]string, len(wecomDeptByID))
	for _, user := range users {
		for _, deptID := range user.LeaderDeptIDs {
			leaders[deptID] = append(leaders[deptID], user.UserID)
		}
	}
	for deptID := range leaders {
		sort.Strings(leaders[deptID])
	}

	deptParent := make(map[int64]int64, len(wecomDeptByID))
	for id, dept := range wecomDeptByID {
		deptParent[id] = dept.ParentID
	}

	// 企微内部邮箱重复的成员无法建号（本地 Email 唯一）
	wecomEmailCount := make(map[string]int, len(users))
	for _, user := range users {
		if email := normalizeEmail(user.Email); email != "" {
			wecomEmailCount[email]++
		}
	}

	presentUserIDs := make(map[string]bool, len(users))
	claimedLocalIDs := make(map[uint]bool, len(local.Employees))

	// 按 UserID 排序，保证每次输出稳定（便于 diff 和测试）
	sortedUsers := make([]User, len(users))
	copy(sortedUsers, users)
	sort.Slice(sortedUsers, func(i, j int) bool { return sortedUsers[i].UserID < sortedUsers[j].UserID })

	// 被停用或已离职的成员：只停用，不同步资料
	var deactivated []uint

	for _, user := range sortedUsers {
		presentUserIDs[user.UserID] = true

		matched := localEmpByWeCom[user.UserID]

		if !user.IsEnabled || user.Status == userStatusDeactivated {
			if matched != nil && matched.IsActive {
				deactivated = append(deactivated, matched.ID)
			}
			continue
		}

		if user.Status == userStatusUnactivated {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf(
				"成员 %s(%s) 尚未激活企业微信，已跳过", user.Name, user.UserID))
			continue
		}

		// 所属部门：本地只支持一个部门，取第一个有效部门
		var deptIDs []int64
		for _, deptID := range user.DeptIDs {
			if _, ok := wecomDeptByID[deptID]; ok {
				deptIDs = append(deptIDs, deptID)
			}
		}
		if len(deptIDs) == 0 {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf(
				"成员 %s(%s) 不在任何已同步的部门内，已跳过", user.Name, user.UserID))
			continue
		}
		mainDeptID := deptIDs[0]
		if len(deptIDs) > 1 {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf(
				"成员 %s(%s) 同时属于 %d 个部门，本地只保留第一个（部门 %d）",
				user.Name, user.UserID, len(deptIDs), mainDeptID))
		}
		mainDeptExternalID := strconv.FormatInt(mainDeptID, 10)

		// 上级推断
		managerUserID, why := resolveManager(user.UserID, mainDeptID, leaders, deptParent)
		if why != "" {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("成员 %s(%s)：%s", user.Name, user.UserID, why))
		}

		email := normalizeEmail(user.Email)

		// 匹配本地账号
		if matched == nil && email != "" {
			candidates := localEmpByEmail[email]
			if len(candidates) == 1 && !claimedLocalIDs[candidates[0].ID] {
				matched = candidates[0]
			} else if len(candidates) > 1 {
				plan.Warnings = append(plan.Warnings, fmt.Sprintf(
					"成员 %s(%s) 的邮箱 %s 在本地对应多个账号，已跳过", user.Name, user.UserID, email))
				continue
			}
		}

		if matched != nil {
			claimedLocalIDs[matched.ID] = true

			update := EmployeeUpdate{LocalID: matched.ID, WeComUserID: user.UserID}

			if user.Name != "" && matched.Name != user.Name {
				name := user.Name
				update.Name = &name
			}
			// 不主动清空本地邮箱：Email 是非空唯一列，清空会触发唯一约束
			if email != "" && normalizeEmail(matched.Email) != email {
				value := email
				update.Email = &value
			}
			if matched.Position != user.Position {
				position := user.Position
				update.Position = &position
			}
			if currentDeptExternal(local, matched.DeptID) != mainDeptExternalID {
				dept := mainDeptExternalID
				update.DeptExternalID = &dept
			}

			// 上级：默认只在本地没有上级时补位
			if managerUserID != "" && managerUserID != user.UserID {
				switch {
				case matched.ManagerID == nil:
					update.ManagerSet = true
					update.ManagerWeComUserID = managerUserID
				case opts.OverwriteManager:
					if current := localEmpByID[*matched.ManagerID]; current == nil || current.WeComUserID != managerUserID {
						update.ManagerSet = true
						update.ManagerWeComUserID = managerUserID
					}
				default:
					if current := localEmpByID[*matched.ManagerID]; current != nil && current.WeComUserID != managerUserID {
						plan.Warnings = append(plan.Warnings, fmt.Sprintf(
							"成员 %s(%s) 的企微上级是 %s，本地已有上级 %s，保留本地",
							user.Name, user.UserID, managerUserID, current.Name))
					}
				}
			}

			// 角色：部门负责人自动成为 manager（只升不降）
			if opts.PromoteDeptLeaders && isDeptLeader(user.UserID, leaders) && matched.Role == "employee" {
				role := "manager"
				update.Role = &role
			}

			// 没有上级不影响登录，只影响能否被创建考核，提醒 HR 补人
			if matched.ManagerID == nil && managerUserID == "" {
				plan.Warnings = append(plan.Warnings, fmt.Sprintf(
					"成员 %s(%s) 没有直属上级，补齐前无法为其创建考核", user.Name, user.UserID))
			}

			if update.Name != nil || update.Email != nil || update.Position != nil ||
				update.DeptExternalID != nil || update.ManagerSet || update.Role != nil ||
				matched.WeComUserID == "" {
				plan.EmployeeUpdates = append(plan.EmployeeUpdates, update)
			}
			continue
		}

		// 新建
		if email == "" {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf(
				"成员 %s(%s) 没有邮箱，无法建号（本地邮箱为必填唯一字段）", user.Name, user.UserID))
			continue
		}
		if wecomEmailCount[email] > 1 {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf(
				"成员 %s(%s) 的邮箱 %s 在企微内重复，无法建号", user.Name, user.UserID, email))
			continue
		}
		if len(localEmpByEmail[email]) > 0 {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf(
				"成员 %s(%s) 的邮箱 %s 已被本地其他账号占用，无法建号", user.Name, user.UserID, email))
			continue
		}

		create := EmployeeCreate{
			WeComUserID:        user.UserID,
			Name:               user.Name,
			Email:              email,
			Position:           user.Position,
			DeptExternalID:     mainDeptExternalID,
			ManagerWeComUserID: managerUserID,
			Role:               "employee",
		}
		if opts.PromoteDeptLeaders && isDeptLeader(user.UserID, leaders) {
			create.Role = "manager"
		}
		// 账号一律正常创建（可登录）。没有上级只影响能否被创建考核，交给 HR 补。
		if managerUserID == "" {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf(
				"成员 %s(%s) 未找到上级，已正常建号；补齐直属上级前无法为其创建考核", user.Name, user.UserID))
		}

		plan.EmployeeCreates = append(plan.EmployeeCreates, create)
	}

	// 本地已绑定企微、但通讯录里彻底找不到的员工 → 停用
	for _, emp := range local.Employees {
		if emp.WeComUserID == "" || presentUserIDs[emp.WeComUserID] || !emp.IsActive {
			continue
		}
		deactivated = append(deactivated, emp.ID)
		plan.Warnings = append(plan.Warnings, fmt.Sprintf(
			"员工 %s 已不在企业微信通讯录中，已计划停用", emp.Name))
	}

	plan.DeactivatedLocalIDs = dedupeUint(deactivated)

	return plan
}

// deptCreateDepth 计算某次部门新建的层级深度（根部门为 0），用于排序。
func deptCreateDepth(create DeptCreate, depts map[int64]Dept) int {
	depth := 1
	current := create.ExternalID
	for i := 0; i < 64; i++ {
		id, err := strconv.ParseInt(current, 10, 64)
		if err != nil {
			return depth
		}
		dept, ok := depts[id]
		if !ok || dept.ParentID == rootDeptID || dept.ParentID == 0 {
			return depth
		}
		current = strconv.FormatInt(dept.ParentID, 10)
		depth++
	}
	return depth
}

// resolveManager 从成员所在部门向上找第一个「唯一负责人」。
//
// 如果成员本人就是当前部门的负责人，则跳过当前部门继续向上找——
// 部门负责人的上级在上一级部门，而不是同部门的其他负责人（否则会形成互为上级的环）。
//
// 返回的第二个值非空时表示无法唯一确定（例如同一部门有多个负责人），
// 调用方应把它变成警告交给 HR，而不是随便挑一个。
func resolveManager(
	self string,
	startDept int64,
	leaders map[int64][]string,
	parentOf map[int64]int64,
) (manager, reason string) {
	dept := startDept

	for depth := 0; depth < 64; depth++ {
		if !containsUser(leaders[dept], self) {
			candidates := leaders[dept]
			switch len(candidates) {
			case 1:
				return candidates[0], ""
			case 0:
				// 该部门没有负责人，继续向上一级找
			default:
				return "", fmt.Sprintf("部门 %d 有多个负责人（%s），无法确定唯一上级",
					dept, strings.Join(candidates, "、"))
			}
		}

		parent, ok := parentOf[dept]
		if !ok || parent == 0 || parent == dept {
			return "", ""
		}
		dept = parent
	}

	return "", ""
}

func containsUser(userIDs []string, target string) bool {
	for _, userID := range userIDs {
		if userID == target {
			return true
		}
	}
	return false
}

// isDeptLeader 判断成员是否至少是一个部门的负责人。
func isDeptLeader(userID string, leaders map[int64][]string) bool {
	for _, userIDs := range leaders {
		for _, id := range userIDs {
			if id == userID {
				return true
			}
		}
	}
	return false
}

// currentDeptExternal 返回本地员工当前部门对应的企微部门 ID。
func currentDeptExternal(local LocalState, deptID uint) string {
	for _, dept := range local.Depts {
		if dept.ID == deptID {
			return dept.ExternalID
		}
	}
	return ""
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func dedupeUint(values []uint) []uint {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[uint]bool, len(values))
	result := make([]uint, 0, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

// 企微成员状态（与 SDK 的 workwx.UserStatus 对应）
const (
	userStatusDeactivated = 2
	userStatusUnactivated = 4
)
