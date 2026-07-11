import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { localUpload, setLocalViewAsUser } from '../../api/client'

// Upload state: tracks the in-flight transfer for the progress banner. The
// AbortController lets the user cancel mid-stream; the hidden <input> is reset
// after each pick so re-selecting the same file fires onChange again.
export function useLocalUpload(activeMount: string, path: string, viewAsUser: string, refresh: () => void) {
  const { t } = useTranslation()
  const [upload, setUpload] = useState<{ name: string; loaded: number; total: number } | null>(null)
  const [uploadError, setUploadError] = useState('')
  const uploadAbortRef = useRef<AbortController | null>(null)
  const fileInputRef = useRef<HTMLInputElement | null>(null)

  const handleUploadPick = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    // Reset the input so picking the same file again re-fires onChange.
    e.target.value = ''
    if (!file || !activeMount) return

    const controller = new AbortController()
    uploadAbortRef.current = controller
    setUploadError('')
    setUpload({ name: file.name, loaded: 0, total: file.size })
    setLocalViewAsUser(viewAsUser) // keep admin "view as user" scoping consistent
    try {
      await localUpload(
        activeMount,
        path,
        file,
        (loaded, total) => setUpload({ name: file.name, loaded, total }),
        controller.signal,
      )
      setUpload(null)
      refresh()
    } catch (err: any) {
      if (controller.signal.aborted) {
        setUploadError(t('local.upload.canceled'))
      } else {
        setUploadError(err?.response?.data?.error || err?.message || t('local.errors.upload'))
      }
      setUpload(null)
    } finally {
      uploadAbortRef.current = null
    }
  }

  return { upload, uploadError, setUploadError, uploadAbortRef, fileInputRef, handleUploadPick }
}
