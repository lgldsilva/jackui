import { TorrentInfo } from '../../api/client'

export function computeFilePickerState(params: {
  info: TorrentInfo | null
  minimized: boolean
  sidebarOpen: boolean
  aggregateMode: boolean
}) {
  const { info, minimized, sidebarOpen, aggregateMode } = params

  const fileCount = info?.files?.length ?? 0
  const hasMultipleFiles = fileCount > 1

  const showFilePicker = !minimized && hasMultipleFiles && sidebarOpen && !aggregateMode
  const showReopenTab = (aggregateMode || hasMultipleFiles) && !sidebarOpen

  return {
    showFilePicker,
    showReopenTab,
    fileCount,
  }
}
