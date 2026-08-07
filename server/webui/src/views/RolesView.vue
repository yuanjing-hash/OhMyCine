<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { api } from '@/api/client'
import { Permissions, type PermissionCode } from '@/auth/generated-permissions'
import { useAuthStore } from '@/stores/auth'
import type { ListResponse, PermissionDefinition, RoleSummary } from '@/types/api'

const auth = useAuthStore(); const roles = ref<RoleSummary[]>([]); const permissions = ref<PermissionDefinition[]>([]); const selectedID = ref<number | null>(null)
const loading = ref(true); const saving = ref(false); const error = ref(''); const notice = ref(''); const createOpen = ref(false)
const createForm = ref({ code: '', name: '', description: '' }); const roleName = ref(''); const roleDescription = ref(''); const roleActive = ref(true); const selectedPermissions = ref<PermissionCode[]>([])
const selected = computed(() => roles.value.find(role => role.id === selectedID.value) ?? null)
const grouped = computed(() => Object.entries(permissions.value.reduce<Record<string, PermissionDefinition[]>>((result, permission) => { (result[permission.module] ??= []).push(permission); return result }, {})))
watch(selected, role => { roleName.value = role?.name ?? ''; roleDescription.value = role?.description ?? ''; roleActive.value = role?.active ?? true; selectedPermissions.value = [...(role?.permissions ?? [])] }, { immediate: true })

async function load() { loading.value = true; error.value = ''; try { const [roleData, permissionData] = await Promise.all([api<ListResponse<RoleSummary>>('/api/v1/roles'), api<PermissionDefinition[]>('/api/v1/permissions')]); roles.value = roleData.list; permissions.value = permissionData; if (!selectedID.value && roles.value.length) selectedID.value = roles.value[0]?.id ?? null } catch (reason) { error.value = message(reason) } finally { loading.value = false } }
async function createRole() { await run(async () => { const created = await api<RoleSummary>('/api/v1/roles', { method: 'POST', body: JSON.stringify({ ...createForm.value, permissions: [] }) }); selectedID.value = created.id; createForm.value = { code: '', name: '', description: '' }; createOpen.value = false; notice.value = '自定义角色已创建' }) }
async function saveRole() { if (!selected.value) return; await run(async () => { await api(`/api/v1/roles/${selected.value?.id}`, { method: 'PATCH', body: JSON.stringify({ name: roleName.value, description: roleDescription.value, active: roleActive.value }) }); notice.value = '角色信息已保存' }) }
async function savePermissions() { if (!selected.value) return; await run(async () => { await api(`/api/v1/roles/${selected.value?.id}/permissions`, { method: 'PUT', body: JSON.stringify({ permissions: selectedPermissions.value }) }); notice.value = '权限矩阵已保存' }) }
async function deleteRole() { if (!selected.value || !window.confirm(`确认删除角色 ${selected.value.name}？`)) return; await run(async () => { await api(`/api/v1/roles/${selected.value?.id}`, { method: 'DELETE', body: '{}' }); selectedID.value = null; notice.value = '角色已删除' }) }
async function run(action: () => Promise<void>) { saving.value = true; error.value = ''; try { await action(); await load() } catch (reason) { error.value = message(reason) } finally { saving.value = false } }
function message(reason: unknown) { return reason instanceof Error ? reason.message : '操作失败' }
function riskClass(risk: PermissionDefinition['risk']) { return risk === 'destructive' ? 'text-red-200' : risk === 'sensitive' ? 'text-amber-200' : 'text-slate-300' }
onMounted(load)
</script>

<template>
  <section>
    <div class="flex flex-wrap items-end justify-between gap-4"><div><p class="mb-2 text-xs font-700 uppercase tracking-[.22em] text-cyan-300">Authorization</p><h1 class="m-0 text-3xl font-800">角色与权限</h1><p class="mt-2 text-slate-400">Permission code 是页面、导航、按钮和 API 的唯一共享契约。</p></div><button v-if="auth.can(Permissions.RolesCreate)" class="btn-primary" @click="createOpen = !createOpen">{{ createOpen ? '取消创建' : '创建角色' }}</button></div>
    <p v-if="error" class="mt-5 rounded-3 bg-red-400/10 p-3 text-sm text-red-200">{{ error }}</p><p v-if="notice" class="mt-5 rounded-3 bg-emerald-400/10 p-3 text-sm text-emerald-200">{{ notice }}</p>
    <form v-if="createOpen" class="panel mt-6 grid gap-4 md:grid-cols-3" @submit.prevent="createRole"><div><label class="label">稳定 code</label><input v-model="createForm.code" class="input" required minlength="3" maxlength="64" placeholder="library_manager" /></div><div><label class="label">名称</label><input v-model="createForm.name" class="input" required maxlength="128" /></div><div><label class="label">说明</label><input v-model="createForm.description" class="input" maxlength="512" /></div><button class="btn-primary md:col-span-3" :disabled="saving">创建空角色</button></form>
    <div v-if="loading" class="mt-8 text-slate-500">正在加载权限目录…</div>
    <div v-else class="mt-7 grid gap-5 xl:grid-cols-[19rem_1fr]">
      <div class="panel p-2"><button v-for="role in roles" :key="role.id" class="mb-1 w-full rounded-3 p-3 text-left transition hover:bg-white/7" :class="selectedID === role.id ? 'bg-white/10' : ''" @click="selectedID = role.id"><div class="flex justify-between gap-2"><strong>{{ role.name }}</strong><span class="text-[10px] uppercase" :class="role.active ? 'text-emerald-300' : 'text-slate-600'">{{ role.protected ? 'SYSTEM' : role.active ? 'ACTIVE' : 'DISABLED' }}</span></div><div class="mt-1 text-xs text-slate-500">{{ role.code }} · {{ role.user_count }} 用户 · {{ role.permissions.length }} 权限</div></button></div>
      <section v-if="selected" class="space-y-5">
        <form class="panel" @submit.prevent="saveRole"><div class="flex items-center justify-between"><h2 class="m-0">{{ selected.name }}</h2><span class="rounded-full bg-white/8 px-3 py-1 text-xs text-slate-400">{{ selected.kind }}</span></div><div class="mt-5 grid gap-4 md:grid-cols-2"><div><label class="label">名称</label><input v-model="roleName" class="input" :disabled="selected.protected || !auth.can(Permissions.RolesUpdate)" /></div><div><label class="label">角色 code</label><input :value="selected.code" class="input" disabled /></div><div class="md:col-span-2"><label class="label">说明</label><textarea v-model="roleDescription" class="input min-h-20" :disabled="selected.protected || !auth.can(Permissions.RolesUpdate)"></textarea></div><label class="flex items-center gap-3 text-sm text-slate-300"><input v-model="roleActive" type="checkbox" :disabled="selected.protected || !auth.can(Permissions.RolesUpdate)" />启用此角色</label></div><div v-if="!selected.protected" class="mt-5 flex gap-3"><button v-if="auth.can(Permissions.RolesUpdate)" class="btn-secondary" :disabled="saving">保存角色</button><button v-if="auth.can(Permissions.RolesDelete)" type="button" class="btn-danger" :disabled="saving || selected.user_count > 0" @click="deleteRole">删除角色</button></div><p v-else class="mb-0 mt-5 text-xs text-slate-500">系统角色由版本迁移维护，不能通过管理端删除或修改。</p></form>
        <section class="panel"><div class="flex flex-wrap items-center justify-between gap-3"><div><h2 class="m-0 text-lg">权限矩阵</h2><p class="mb-0 mt-1 text-xs text-slate-500">敏感和破坏性权限会明确标色；服务端阻止越权授权。</p></div><button v-if="!selected.protected && auth.can(Permissions.RolesUpdate)" class="btn-primary" :disabled="saving" @click="savePermissions">保存权限</button></div>
          <div class="mt-6 space-y-6"><fieldset v-for="[module, items] in grouped" :key="module" class="m-0 border-0 p-0"><legend class="mb-3 text-xs font-700 uppercase tracking-[.18em] text-cyan-300">{{ module }}</legend><div class="grid gap-2 md:grid-cols-2"><label v-for="permission in items" :key="permission.code" class="flex gap-3 rounded-3 border border-white/7 bg-white/3 p-3" :class="selected.protected || !auth.can(Permissions.RolesUpdate) ? 'opacity-70' : 'hover:bg-white/6'"><input v-model="selectedPermissions" type="checkbox" :value="permission.code" :disabled="selected.protected || !auth.can(Permissions.RolesUpdate)" /><span><strong class="block text-sm" :class="riskClass(permission.risk)">{{ permission.name }}</strong><code class="text-[11px] text-slate-500">{{ permission.code }}</code><small class="mt-1 block leading-5 text-slate-500">{{ permission.description }}</small></span></label></div></fieldset></div>
        </section>
      </section>
    </div>
  </section>
</template>
