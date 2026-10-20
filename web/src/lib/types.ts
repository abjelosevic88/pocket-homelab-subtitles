export interface User { id: number; email: string; name: string; role: 'admin' | 'member'; created_at: string }
export interface APIToken { id: number; name: string; prefix: string; last_used_at: string | null; created_at: string }

export interface Subtitle {
  id: number; path: string; video_path: string; rel: string; library: string; title: string; kind: 'episode' | 'movie' | 'other'
  season: number | null; episode: number | null; label: string; lang: string; flags: string
  last_decision: string; last_reason: string; last_method: string; last_shift: number | null; last_run_id: number | null; last_checked_at: string | null
  pinned: boolean; pin_note: string; pinned_at: string | null; nudge_ms: number
  verify: VerifyResult | null; verified_at: string | null; missing: boolean; seen_at: string | null; created_at: string
}

export interface VerifyResult { ok: boolean; offset: number | null; target: number; tolerance: number; problems: string[]; tracks: { track: number; ok: boolean; offset: number | null; why: string }[] }

export interface Run {
  id: number; subtitle_id: number | null; path: string; name: string; title: string; label: string; lang: string; source: string
  decision: string; method: string; max_shift: number | null; content_fit: number | null; ref_fit: number | null; reason: string
  duration_ms: number; details?: Record<string, unknown>; job_id: number | null; created_at: string
}

export interface Job {
  id: number; path: string; video_path: string; title: string; label: string; lang: string; source: string; priority: number
  extras: Record<string, string> | null; force: boolean; state: string; stage: string; run_id: number | null; task_id: number | null
  error: string; created_at: string; started_at: string | null; finished_at: string | null; decision?: string
}
export interface RunningJob extends Job { elapsed_s: number }

export interface Task {
  id: number; kind: string; title: string; state: string; params: Record<string, unknown>; summary: Record<string, unknown>
  log?: string; total: number; done: number; user_id: number | null; created_at: string; finished_at: string | null
}

export interface TitleSummary { title: string; library: string; kind: string; files: number; langs: string[]; seasons: number[] | null; attention: number; pinned: number; synced: number; unchecked: number; last_at: string | null }

export interface DayCount { day: string; adjusted: number; in_sync: number; problems: number }

export interface Overview {
  stats: {
    processed: number; adjusted: number; in_sync: number; rejected: number; failed: number; today: number
    files: number; titles: number; attention: number; pinned: number; nudged: number; unchecked: number; missing: number
    queued: number; running: number; missing_downloads: number | null; missing_episodes: number; missing_movies: number
  }
  running: RunningJob[]; next: Job[]; queued_total: number; recent: Run[]; days: DayCount[]
  sweep: { enabled: boolean; time: string; budget: number; running: boolean; next_at?: string; last?: Task }
  last_scan: string | null
}

export interface FileDetail extends Subtitle { runs: Run[]; events: FileEvent[]; has_original: boolean; can_undo: boolean; queued: Job | null; exists: boolean }
export interface FileEvent { id: number; path: string; kind: string; detail: string; user_id: number | null; created_at: string }

export type ConnMode = 'core' | 'direct' | 'off'
export interface Settings {
  title: string; date_format: string; time_format: string; setup_complete: boolean
  lead_ms: number; centre_ref: boolean; polish: boolean; ref_residual: boolean; content_gate: boolean
  mt_gate_langs: string; latinize_langs: string; on_reject: 'blacklist' | 'quarantine' | 'keep'; verify_tol_ms: number; languages: string; auto_unpin_new: boolean
  sweep_enabled: boolean; sweep_time: string; sweep_budget: number; reverify_before: string; last_sweep_at: string
  bazarr_url: string; bazarr_api_key_set: boolean; bazarr_path_map: string
  jellyfin_url: string; jellyfin_api_key_set: boolean; jellyfin_path_map: string
  bazarr_mode: ConnMode; bazarr_via?: string; bazarr_source?: string; bazarr_detail?: string
  jellyfin_mode: ConnMode; jellyfin_via?: string; jellyfin_source?: string; jellyfin_detail?: string
  services_as?: string; core_configured?: boolean
  notify_kind: '' | 'apprise' | 'ntfy' | 'webhook'; notify_url: string; notify_failures: boolean; notify_synced: boolean; notify_sweep: boolean
  season_langs: string; season_manual_langs: string; prefer_provider: string
}

export interface WantedItem { kind: 'episode' | 'movie'; series_id?: number; episode_id?: number; movie_id?: number; title: string; detail: string; languages: string[] | null }
export interface Nudge { id: number; path: string; ms: number; backup: string; cues: number; undone: boolean; user_id: number | null; created_at: string }
