export interface User {
  id: number;
  username: string;
  email: string;
  created_at: string;
  google_linked?: boolean;
  sso_enabled?: boolean;
}

export interface NodeItem {
  id: number;
  user_id: number;
  path: string;
  parent_path: string;
  name: string;
  is_dir: boolean;
  size: number;
  etag: string;
  file_id: string;
  mtime: string;
  content_type: string;
  created_at: string;
  updated_at: string;
}

export interface Share {
  id: number;
  token: string;
  path: string;
  is_dir: boolean;
  share_url: string;
  created_at: string;
}

export interface PublicShareInfo {
  token: string;
  name: string;
  path: string;
  is_dir: boolean;
  size: number;
  mtime: string;
  content_type: string;
}
