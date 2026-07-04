import { useState } from 'react'
import { Folder, FileVideo, FileAudio, File as FileIcon } from 'lucide-react'
import { LocalEntry, localThumbURL } from '../../api/client'
import { isVideo, isAudio } from './entryFormat'

export function EntryIcon({ entry, mount }: { readonly entry: LocalEntry; readonly mount: string }) {
  const [thumbFailed, setThumbFailed] = useState(false)
  if (entry.isDir) return <Folder className="w-5 h-5 text-blue-400 flex-shrink-0" />
  if (isVideo(entry.name)) {
    // Early-frame preview (lazy); falls back to the icon if the server can't
    // decode one (204/error). Fixed 16:9 box keeps rows aligned.
    if (thumbFailed) return <FileVideo className="w-5 h-5 text-purple-400 flex-shrink-0" />
    return (
      <img
        src={localThumbURL(mount, entry.path)}
        alt=""
        loading="lazy"
        onError={() => setThumbFailed(true)}
        className="w-14 h-8 object-cover rounded bg-surface border border-default flex-shrink-0"
      />
    )
  }
  if (isAudio(entry.name)) return <FileAudio className="w-5 h-5 text-pink-400 flex-shrink-0" />
  return <FileIcon className="w-5 h-5 text-text-secondary flex-shrink-0" />
}
