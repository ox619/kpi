package wecom

import (
	"strings"
	"testing"
)

// 这些测试只覆盖「规划」这一层，不需要企业微信环境，也不碰数据库。
// 覆盖的都是真正会出问题的判断：上级推断、多负责人、缺邮箱、停用、手工调整不被覆盖。

const (
	deptTech    int64 = 2 // 技术部（顶级）
	deptBackend int64 = 3 // 后端组（技术部下属）
)

func testDepts() []Dept {
	return []Dept{
		{ID: 1, Name: "示例公司", ParentID: 0},
		{ID: deptTech, Name: "技术部", ParentID: 1, Order: 1},
		{ID: deptBackend, Name: "后端组", ParentID: deptTech, Order: 1},
	}
}

// activeUser 构造一个已激活的企微成员；leaderOf 非空表示其在这些部门是负责人。
func activeUser(userID, name, email string, dept int64, leaderOf ...int64) User {
	user := User{
		UserID:    userID,
		Name:      name,
		Email:     email,
		DeptIDs:   []int64{dept},
		IsEnabled: true,
		Status:    1,
	}
	if len(leaderOf) > 0 {
		user.IsLeader = true
		user.LeaderDeptIDs = leaderOf
	}
	return user
}

func hasWarning(plan Plan, substr string) bool {
	for _, warning := range plan.Warnings {
		if strings.Contains(warning, substr) {
			return true
		}
	}
	return false
}

func findCreate(plan Plan, userID string) *EmployeeCreate {
	for i := range plan.EmployeeCreates {
		if plan.EmployeeCreates[i].WeComUserID == userID {
			return &plan.EmployeeCreates[i]
		}
	}
	return nil
}

func findUpdate(plan Plan, localID uint) *EmployeeUpdate {
	for i := range plan.EmployeeUpdates {
		if plan.EmployeeUpdates[i].LocalID == localID {
			return &plan.EmployeeUpdates[i]
		}
	}
	return nil
}

func uintPtr(v uint) *uint { return &v }

// 首次导入：部门按层级新建，成员按部门负责人推断上级；找不到上级的照样建号（可登录），但要提醒 HR 补。
func TestBuildPlanInitialImport(t *testing.T) {
	users := []User{
		activeUser("boss", "张三", "a@example.com", deptTech, deptTech),
		activeUser("dev", "李四", "b@example.com", deptBackend),
	}

	plan := BuildPlan(LocalState{}, users, testDepts(), SyncOptions{})

	if len(plan.DeptCreates) != 2 {
		t.Fatalf("应新建 2 个部门，实际 %d", len(plan.DeptCreates))
	}
	if plan.DeptCreates[0].ExternalID != "2" || plan.DeptCreates[0].ParentExternalID != "" {
		t.Fatalf("顶级部门应在最前且无上级，实际 %+v", plan.DeptCreates[0])
	}
	if plan.DeptCreates[1].ExternalID != "3" || plan.DeptCreates[1].ParentExternalID != "2" {
		t.Fatalf("子部门应挂在技术部下，实际 %+v", plan.DeptCreates[1])
	}

	boss := findCreate(plan, "boss")
	dev := findCreate(plan, "dev")
	if boss == nil || dev == nil {
		t.Fatalf("应新建 2 名员工，实际 %d", len(plan.EmployeeCreates))
	}
	if boss.ManagerWeComUserID != "" {
		t.Errorf("找不到上级时不应编造上级，实际 %s", boss.ManagerWeComUserID)
	}
	if !hasWarning(plan, "无法为其创建考核") {
		t.Errorf("应提示需要补上级，实际 %v", plan.Warnings)
	}
	if dev.ManagerWeComUserID != "boss" {
		t.Errorf("后端组成员上级应为 boss，实际 %+v", dev)
	}
	if dev.DeptExternalID != "3" {
		t.Errorf("员工应落在后端组，实际 %s", dev.DeptExternalID)
	}
}

// 同步落地后再次运行，应不产生任何变更（幂等）。
func TestBuildPlanIsIdempotent(t *testing.T) {
	users := []User{
		activeUser("boss", "张三", "a@example.com", deptTech, deptTech),
		activeUser("dev", "李四", "b@example.com", deptBackend),
	}
	local := LocalState{
		Depts: []LocalDept{
			{ID: 10, Name: "技术部", ExternalID: "2"},
			{ID: 11, Name: "后端组", ParentID: 10, ExternalID: "3"},
		},
		Employees: []LocalEmployee{
			{ID: 100, Name: "张三", Email: "a@example.com", WeComUserID: "boss", DeptID: 10, Role: "employee", IsActive: true},
			{ID: 101, Name: "李四", Email: "b@example.com", WeComUserID: "dev", DeptID: 11, ManagerID: uintPtr(100), Role: "employee", IsActive: true},
		},
	}

	plan := BuildPlan(local, users, testDepts(), SyncOptions{})

	if !plan.Empty() {
		t.Fatalf("二次同步不应产生变更：%s；员工更新=%+v 部门更新=%+v", plan.Summary(), plan.EmployeeUpdates, plan.DeptUpdates)
	}
}

// 本地历史数据是扁平部门（没有 ExternalID），首次同步应按名称关联并补上层级。
func TestBuildPlanMatchesLegacyFlatDepartments(t *testing.T) {
	local := LocalState{
		Depts: []LocalDept{
			{ID: 5, Name: "技术部"},
			{ID: 6, Name: "后端组"},
		},
	}

	users := []User{activeUser("dev", "李四", "b@example.com", deptBackend)}
	plan := BuildPlan(local, users, testDepts(), SyncOptions{})

	if len(plan.DeptCreates) != 0 {
		t.Fatalf("部门已存在，不应新建，实际 %+v", plan.DeptCreates)
	}

	techUpdate := findDeptUpdate(plan, 5)
	backendUpdate := findDeptUpdate(plan, 6)
	if techUpdate == nil || techUpdate.ExternalID != "2" {
		t.Fatalf("技术部应关联企微部门 2，实际 %+v", techUpdate)
	}
	if techUpdate.ParentExternalID != nil {
		t.Errorf("技术部上级未变化，不应出现在更新里，实际 %v", *techUpdate.ParentExternalID)
	}
	if backendUpdate == nil || backendUpdate.ParentExternalID == nil || *backendUpdate.ParentExternalID != "2" {
		t.Fatalf("后端组应挂到技术部下，实际 %+v", backendUpdate)
	}
}

func findDeptUpdate(plan Plan, localID uint) *DeptUpdate {
	for i := range plan.DeptUpdates {
		if plan.DeptUpdates[i].LocalID == localID {
			return &plan.DeptUpdates[i]
		}
	}
	return nil
}

// 同一部门有多个负责人时不能猜，只能交给 HR；负责人之间也不能互为上级。
func TestBuildPlanMultipleLeaders(t *testing.T) {
	users := []User{
		activeUser("a", "甲", "a@example.com", deptTech, deptTech),
		activeUser("b", "乙", "b@example.com", deptTech, deptTech),
		activeUser("c", "丙", "c@example.com", deptTech),
	}

	plan := BuildPlan(LocalState{}, users, testDepts(), SyncOptions{})

	for _, userID := range []string{"a", "b", "c"} {
		create := findCreate(plan, userID)
		if create == nil {
			t.Fatalf("成员 %s 应被新建", userID)
		}
		if userID == "c" && create.ManagerWeComUserID != "" {
			t.Errorf("多负责人时不应猜上级，实际 %s", create.ManagerWeComUserID)
		}
		if create.ManagerWeComUserID == "a" || create.ManagerWeComUserID == "b" {
			t.Errorf("负责人之间不应互为上级，%s 的上级是 %s", userID, create.ManagerWeComUserID)
		}
	}
	if !hasWarning(plan, "多个负责人") {
		t.Errorf("应产生多负责人警告，实际 %v", plan.Warnings)
	}
}

// 没有邮箱的成员无法建号（本地 Email 唯一且非空），只能报警告。
func TestBuildPlanSkipsUserWithoutEmail(t *testing.T) {
	users := []User{activeUser("no-mail", "无邮箱", "", deptTech)}

	plan := BuildPlan(LocalState{}, users, testDepts(), SyncOptions{})

	if len(plan.EmployeeCreates) != 0 {
		t.Fatalf("缺邮箱不应建号，实际 %+v", plan.EmployeeCreates)
	}
	if !hasWarning(plan, "没有邮箱") {
		t.Errorf("应提示缺邮箱，实际 %v", plan.Warnings)
	}
}

// 企微停用/移除的成员，本地应同步停用。
func TestBuildPlanDeactivatesLeftUsers(t *testing.T) {
	left := User{UserID: "left", Name: "离职者", DeptIDs: []int64{deptTech}, IsEnabled: false, Status: 2}
	users := []User{activeUser("dev", "在职者", "b@example.com", deptTech), left}

	local := LocalState{
		Depts: []LocalDept{{ID: 10, Name: "技术部", ExternalID: "2"}},
		Employees: []LocalEmployee{
			{ID: 100, Name: "在职者", Email: "b@example.com", WeComUserID: "dev", DeptID: 10, Role: "employee", IsActive: true},
			{ID: 101, Name: "离职者", Email: "left@example.com", WeComUserID: "left", DeptID: 10, Role: "employee", IsActive: true},
			// 已经不在通讯录里的老员工
			{ID: 102, Name: "消失者", Email: "gone@example.com", WeComUserID: "gone", DeptID: 10, Role: "employee", IsActive: true},
			// 手工建的账号（没有企微绑定）不能被误停用
			{ID: 103, Name: "手工账号", Email: "manual@example.com", DeptID: 10, Role: "hr", IsActive: true},
		},
	}

	plan := BuildPlan(local, users, testDepts(), SyncOptions{})

	if len(plan.DeactivatedLocalIDs) != 2 {
		t.Fatalf("应停用 2 个账号，实际 %v", plan.DeactivatedLocalIDs)
	}
	if plan.DeactivatedLocalIDs[0] != 101 || plan.DeactivatedLocalIDs[1] != 102 {
		t.Errorf("停用名单应为 101、102，实际 %v", plan.DeactivatedLocalIDs)
	}
}

// 通讯录返回空时不能据此停用全部账号。
func TestBuildPlanEmptyDirectoryDoesNotDeactivate(t *testing.T) {
	local := LocalState{
		Depts: []LocalDept{{ID: 10, Name: "技术部", ExternalID: "2"}},
		Employees: []LocalEmployee{
			{ID: 100, Name: "某人", Email: "x@example.com", WeComUserID: "x", DeptID: 10, Role: "employee", IsActive: true},
		},
	}

	plan := BuildPlan(local, nil, testDepts(), SyncOptions{})

	if !plan.Empty() {
		t.Fatalf("通讯录为空时不应产生变更，实际 %s", plan.Summary())
	}
	if !hasWarning(plan, "未返回任何成员") {
		t.Errorf("应给出跳过同步的警告，实际 %v", plan.Warnings)
	}
}

// HR 手工调整的上级默认不会被企微覆盖，但会给出提示。
func TestBuildPlanKeepsManualManager(t *testing.T) {
	users := []User{
		activeUser("a", "主管甲", "a@example.com", deptTech, deptTech),
		activeUser("b", "成员乙", "b@example.com", deptTech),
	}
	local := LocalState{
		Depts: []LocalDept{{ID: 10, Name: "技术部", ExternalID: "2"}},
		Employees: []LocalEmployee{
			{ID: 100, Name: "主管甲", Email: "a@example.com", WeComUserID: "a", DeptID: 10, Role: "employee", IsActive: true},
			// 乙的上级被 HR 手工设成了 200（丙）
			{ID: 101, Name: "成员乙", Email: "b@example.com", WeComUserID: "b", DeptID: 10, ManagerID: uintPtr(200), Role: "employee", IsActive: true},
			{ID: 200, Name: "丙", Email: "c@example.com", WeComUserID: "c", DeptID: 10, Role: "manager", IsActive: true},
		},
	}

	plan := BuildPlan(local, users, testDepts(), SyncOptions{})
	if update := findUpdate(plan, 101); update != nil && update.ManagerSet {
		t.Fatalf("默认不应覆盖手工设置的上级，实际 %+v", update)
	}
	if !hasWarning(plan, "保留本地") {
		t.Errorf("应提示上级不一致，实际 %v", plan.Warnings)
	}

	overwritten := BuildPlan(local, users, testDepts(), SyncOptions{OverwriteManager: true})
	update := findUpdate(overwritten, 101)
	if update == nil || !update.ManagerSet || update.ManagerWeComUserID != "a" {
		t.Fatalf("开启 OverwriteManager 后应改为企微推断的上级，实际 %+v", update)
	}
}

// 子部门负责人应汇报给上级部门的负责人。
func TestBuildPlanLeaderChain(t *testing.T) {
	users := []User{
		activeUser("a", "技术部负责人", "a@example.com", deptTech, deptTech),
		activeUser("b", "后端组负责人", "b@example.com", deptBackend, deptBackend),
		activeUser("c", "后端成员", "c@example.com", deptBackend),
	}

	plan := BuildPlan(LocalState{}, users, testDepts(), SyncOptions{})

	if b := findCreate(plan, "b"); b == nil || b.ManagerWeComUserID != "a" {
		t.Fatalf("后端组负责人应汇报给技术部负责人，实际 %+v", b)
	}
	if c := findCreate(plan, "c"); c == nil || c.ManagerWeComUserID != "b" {
		t.Fatalf("后端成员应汇报给后端组负责人，实际 %+v", c)
	}
}

// 部门负责人自动获得 manager 角色，但不会把 hr 降级。
func TestBuildPlanPromotesDeptLeader(t *testing.T) {
	users := []User{
		activeUser("a", "主管甲", "a@example.com", deptTech, deptTech),
		activeUser("h", "人事", "h@example.com", deptTech),
	}
	local := LocalState{
		Depts: []LocalDept{{ID: 10, Name: "技术部", ExternalID: "2"}},
		Employees: []LocalEmployee{
			{ID: 100, Name: "主管甲", Email: "a@example.com", WeComUserID: "a", DeptID: 10, ManagerID: uintPtr(100), Role: "employee", IsActive: true},
			{ID: 101, Name: "人事", Email: "h@example.com", WeComUserID: "h", DeptID: 10, ManagerID: uintPtr(100), Role: "hr", IsActive: true},
		},
	}

	plan := BuildPlan(local, users, testDepts(), SyncOptions{PromoteDeptLeaders: true})

	leaderUpdate := findUpdate(plan, 100)
	if leaderUpdate == nil || leaderUpdate.Role == nil || *leaderUpdate.Role != "manager" {
		t.Fatalf("部门负责人应升为 manager，实际 %+v", leaderUpdate)
	}
	if hrUpdate := findUpdate(plan, 101); hrUpdate != nil && hrUpdate.Role != nil {
		t.Fatalf("不应改动 hr 角色，实际 %+v", hrUpdate)
	}
}
