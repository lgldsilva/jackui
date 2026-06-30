import { FileVideo, FileAudio, FileText } from 'lucide-react'

// Shared file-type glyph for the download file pickers (flat list + tree).
// Was duplicated verbatim in DownloadModal and AddTorrentModal; lives here now
// so both pickers and SelectableFileTree show the same icon.
export function fileIcon(f: { path: string; isVideo: boolean }) {
  if (f.isVideo) return <FileVideo className="w-4 h-4 text-green-400 flex-shrink-0" />
  if (/\.(mp3|flac|ogg|wav|m4a|aac|opus)$/i.test(f.path)) {
    return <FileAudio className="w-4 h-4 text-purple-400 flex-shrink-0" />
  }
  return <FileText className="w-4 h-4 text-text-muted flex-shrink-0" />
}
