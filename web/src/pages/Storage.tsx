import { useState, useCallback, useRef } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { storage } from '../lib/api'
import { useAuthStore } from '../store/auth'
import {
  HardDrive,
  Plus,
  Trash2,
  Loader2,
  X,
  Upload,
  File,
  Folder,
  FolderPlus,
  Image,
  FileText,
  Film,
  Music,
  Archive,
  ChevronRight,
  ChevronLeft,
  Eye,
  Download,
  Link,
  Lock,
  Globe,
} from 'lucide-react'

interface BucketInfo {
  name: string
  creation_date: string
  policy?: string
}

interface ObjectInfo {
  key: string
  size: number
  last_modified: string
  content_type: string
  etag: string
  is_dir: boolean
}

const formatBytes = (bytes: number): string => {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i]
}

const formatDate = (dateString: string): string => {
  return new Date(dateString).toLocaleDateString('en-US', {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

const getFileIcon = (contentType: string, isDir: boolean) => {
  if (isDir) return Folder
  if (contentType.startsWith('image/')) return Image
  if (contentType.startsWith('video/')) return Film
  if (contentType.startsWith('audio/')) return Music
  if (contentType.includes('zip') || contentType.includes('tar') || contentType.includes('gzip')) return Archive
  if (contentType.includes('text') || contentType.includes('json') || contentType.includes('xml')) return FileText
  return File
}

const isImageFile = (contentType: string): boolean => {
  return contentType.startsWith('image/')
}

export default function Storage() {
  const queryClient = useQueryClient()
  const { token } = useAuthStore()
  const fileInputRef = useRef<HTMLInputElement>(null)

  const [selectedBucket, setSelectedBucket] = useState<string | null>(null)
  const [currentPath, setCurrentPath] = useState<string>('')
  const [showCreateBucket, setShowCreateBucket] = useState(false)
  const [showCreateFolder, setShowCreateFolder] = useState(false)
  const [newBucketName, setNewBucketName] = useState('')
  const [newBucketPublic, setNewBucketPublic] = useState(false)
  const [newFolderName, setNewFolderName] = useState('')
  const [previewImage, setPreviewImage] = useState<{ url: string; name: string } | null>(null)
  const [dragOver, setDragOver] = useState(false)
  const [uploading, setUploading] = useState(false)

  // Queries
  const { data: bucketsData, isLoading: bucketsLoading } = useQuery({
    queryKey: ['storage-buckets'],
    queryFn: storage.listBuckets,
  })

  const { data: objectsData, isLoading: objectsLoading } = useQuery({
    queryKey: ['storage-objects', selectedBucket, currentPath],
    queryFn: () => storage.listObjects(selectedBucket!, currentPath),
    enabled: !!selectedBucket,
  })

  const { data: bucketInfo } = useQuery({
    queryKey: ['storage-bucket-info', selectedBucket],
    queryFn: () => storage.getBucketInfo(selectedBucket!),
    enabled: !!selectedBucket,
  })

  // Mutations
  const createBucketMutation = useMutation({
    mutationFn: () => storage.createBucket(newBucketName, newBucketPublic),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['storage-buckets'] })
      setShowCreateBucket(false)
      setNewBucketName('')
      setNewBucketPublic(false)
    },
  })

  const deleteBucketMutation = useMutation({
    mutationFn: (name: string) => storage.deleteBucket(name, true),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['storage-buckets'] })
      if (selectedBucket) {
        setSelectedBucket(null)
        setCurrentPath('')
      }
    },
  })

  const deleteObjectMutation = useMutation({
    mutationFn: (key: string) => storage.deleteObject(selectedBucket!, key),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['storage-objects', selectedBucket, currentPath] })
    },
  })

  const createFolderMutation = useMutation({
    mutationFn: () => storage.createFolder(selectedBucket!, currentPath + newFolderName),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['storage-objects', selectedBucket, currentPath] })
      setShowCreateFolder(false)
      setNewFolderName('')
    },
  })

  const toggleBucketPolicyMutation = useMutation({
    mutationFn: (isPublic: boolean) => storage.setBucketPolicy(selectedBucket!, isPublic),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['storage-bucket-info', selectedBucket] })
    },
  })

  // Handlers
  const handleFileUpload = useCallback(async (files: FileList | null) => {
    if (!files || !selectedBucket) return

    setUploading(true)
    try {
      for (const file of Array.from(files)) {
        await storage.uploadObject(selectedBucket, file, currentPath)
      }
      queryClient.invalidateQueries({ queryKey: ['storage-objects', selectedBucket, currentPath] })
    } catch (error) {
      console.error('Upload failed:', error)
    } finally {
      setUploading(false)
    }
  }, [selectedBucket, currentPath, queryClient])

  const handleDrop = useCallback((e: React.DragEvent) => {
    e.preventDefault()
    setDragOver(false)
    handleFileUpload(e.dataTransfer.files)
  }, [handleFileUpload])

  const handleDragOver = useCallback((e: React.DragEvent) => {
    e.preventDefault()
    setDragOver(true)
  }, [])

  const handleDragLeave = useCallback((e: React.DragEvent) => {
    e.preventDefault()
    setDragOver(false)
  }, [])

  const navigateToFolder = (folderKey: string) => {
    setCurrentPath(folderKey)
  }

  const navigateUp = () => {
    const parts = currentPath.split('/').filter(Boolean)
    parts.pop()
    setCurrentPath(parts.length > 0 ? parts.join('/') + '/' : '')
  }

  const handleObjectClick = (obj: ObjectInfo) => {
    if (obj.is_dir) {
      navigateToFolder(obj.key)
    } else if (isImageFile(obj.content_type)) {
      const url = `/api/dashboard/storage/buckets/${selectedBucket}/objects/${obj.key}`
      setPreviewImage({ url, name: obj.key.split('/').pop() || obj.key })
    }
  }

  const handleDownload = async (obj: ObjectInfo) => {
    const url = `/api/dashboard/storage/buckets/${selectedBucket}/objects/${obj.key}`
    const response = await fetch(url, {
      headers: { Authorization: `Bearer ${token}` },
    })
    const blob = await response.blob()
    const downloadUrl = window.URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = downloadUrl
    a.download = obj.key.split('/').pop() || obj.key
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    window.URL.revokeObjectURL(downloadUrl)
  }

  const handleCopyUrl = async (obj: ObjectInfo) => {
    try {
      const { url } = await storage.getPresignedUrl(selectedBucket!, obj.key, 60)
      await navigator.clipboard.writeText(url)
      alert('URL copied to clipboard (expires in 1 hour)')
    } catch (error) {
      console.error('Failed to copy URL:', error)
    }
  }

  const buckets = bucketsData?.buckets || []
  const objects = objectsData?.objects || []
  const pathParts = currentPath.split('/').filter(Boolean)

  return (
    <div className="h-full">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-2xl font-bold text-gray-900">Storage</h1>
          <p className="text-gray-600 mt-1">Manage files and buckets</p>
        </div>
        <button
          onClick={() => setShowCreateBucket(true)}
          className="flex items-center gap-2 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 transition-colors"
        >
          <Plus className="w-4 h-4" />
          New Bucket
        </button>
      </div>

      <div className="flex gap-6 h-[calc(100vh-220px)]">
        {/* Buckets Sidebar */}
        <div className="w-64 flex-shrink-0 bg-white rounded-xl border border-gray-200 overflow-hidden flex flex-col">
          <div className="px-4 py-3 border-b border-gray-200 bg-gray-50">
            <h2 className="font-medium text-gray-900 flex items-center gap-2">
              <HardDrive className="w-4 h-4" />
              Buckets
            </h2>
          </div>
          <div className="flex-1 overflow-y-auto">
            {bucketsLoading ? (
              <div className="flex items-center justify-center py-8">
                <Loader2 className="w-6 h-6 text-blue-600 animate-spin" />
              </div>
            ) : buckets.length === 0 ? (
              <div className="text-center py-8 px-4">
                <HardDrive className="w-10 h-10 text-gray-300 mx-auto mb-2" />
                <p className="text-sm text-gray-500">No buckets yet</p>
              </div>
            ) : (
              <div className="p-2">
                {buckets.map((bucket: BucketInfo) => (
                  <div
                    key={bucket.name}
                    className={`group flex items-center justify-between px-3 py-2 rounded-lg cursor-pointer transition-colors ${
                      selectedBucket === bucket.name
                        ? 'bg-blue-50 text-blue-700'
                        : 'hover:bg-gray-50 text-gray-700'
                    }`}
                    onClick={() => {
                      setSelectedBucket(bucket.name)
                      setCurrentPath('')
                    }}
                  >
                    <div className="flex items-center gap-2 min-w-0">
                      <Folder className="w-4 h-4 flex-shrink-0" />
                      <span className="text-sm font-medium truncate">{bucket.name}</span>
                    </div>
                    <button
                      onClick={(e) => {
                        e.stopPropagation()
                        if (confirm(`Delete bucket "${bucket.name}" and all its contents?`)) {
                          deleteBucketMutation.mutate(bucket.name)
                        }
                      }}
                      className="p-1 text-gray-400 hover:text-red-600 opacity-0 group-hover:opacity-100 transition-opacity"
                    >
                      <Trash2 className="w-4 h-4" />
                    </button>
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>

        {/* Objects Panel */}
        <div className="flex-1 bg-white rounded-xl border border-gray-200 overflow-hidden flex flex-col">
          {!selectedBucket ? (
            <div className="flex-1 flex items-center justify-center">
              <div className="text-center">
                <Folder className="w-16 h-16 text-gray-300 mx-auto mb-4" />
                <h3 className="text-lg font-medium text-gray-900 mb-2">Select a bucket</h3>
                <p className="text-gray-500">Choose a bucket from the sidebar to view its contents</p>
              </div>
            </div>
          ) : (
            <>
              {/* Toolbar */}
              <div className="px-4 py-3 border-b border-gray-200 bg-gray-50 flex items-center justify-between">
                <div className="flex items-center gap-2">
                  {/* Breadcrumb */}
                  <button
                    onClick={() => setCurrentPath('')}
                    className={`text-sm font-medium ${currentPath ? 'text-blue-600 hover:text-blue-700' : 'text-gray-900'}`}
                  >
                    {selectedBucket}
                  </button>
                  {pathParts.map((part, index) => (
                    <div key={index} className="flex items-center gap-2">
                      <ChevronRight className="w-4 h-4 text-gray-400" />
                      <button
                        onClick={() => setCurrentPath(pathParts.slice(0, index + 1).join('/') + '/')}
                        className={`text-sm font-medium ${
                          index === pathParts.length - 1 ? 'text-gray-900' : 'text-blue-600 hover:text-blue-700'
                        }`}
                      >
                        {part}
                      </button>
                    </div>
                  ))}
                </div>
                <div className="flex items-center gap-2">
                  {/* Bucket Policy Toggle */}
                  <button
                    onClick={() => toggleBucketPolicyMutation.mutate(bucketInfo?.policy !== 'public')}
                    className={`flex items-center gap-1.5 px-3 py-1.5 text-sm rounded-lg border transition-colors ${
                      bucketInfo?.policy === 'public'
                        ? 'bg-green-50 border-green-200 text-green-700'
                        : 'bg-gray-50 border-gray-200 text-gray-700'
                    }`}
                    title={bucketInfo?.policy === 'public' ? 'Public bucket' : 'Private bucket'}
                  >
                    {bucketInfo?.policy === 'public' ? (
                      <>
                        <Globe className="w-4 h-4" />
                        Public
                      </>
                    ) : (
                      <>
                        <Lock className="w-4 h-4" />
                        Private
                      </>
                    )}
                  </button>
                  {currentPath && (
                    <button
                      onClick={navigateUp}
                      className="flex items-center gap-1 px-3 py-1.5 text-sm text-gray-700 hover:bg-gray-100 rounded-lg transition-colors"
                    >
                      <ChevronLeft className="w-4 h-4" />
                      Back
                    </button>
                  )}
                  <button
                    onClick={() => setShowCreateFolder(true)}
                    className="flex items-center gap-1 px-3 py-1.5 text-sm text-gray-700 hover:bg-gray-100 rounded-lg border border-gray-200 transition-colors"
                  >
                    <FolderPlus className="w-4 h-4" />
                    New Folder
                  </button>
                  <button
                    onClick={() => fileInputRef.current?.click()}
                    className="flex items-center gap-1 px-3 py-1.5 text-sm bg-blue-600 text-white rounded-lg hover:bg-blue-700 transition-colors"
                  >
                    <Upload className="w-4 h-4" />
                    Upload
                  </button>
                  <input
                    ref={fileInputRef}
                    type="file"
                    multiple
                    className="hidden"
                    onChange={(e) => handleFileUpload(e.target.files)}
                  />
                </div>
              </div>

              {/* Objects List */}
              <div
                className={`flex-1 overflow-y-auto ${dragOver ? 'bg-blue-50' : ''}`}
                onDrop={handleDrop}
                onDragOver={handleDragOver}
                onDragLeave={handleDragLeave}
              >
                {objectsLoading || uploading ? (
                  <div className="flex items-center justify-center py-12">
                    <Loader2 className="w-8 h-8 text-blue-600 animate-spin" />
                    {uploading && <span className="ml-2 text-gray-600">Uploading...</span>}
                  </div>
                ) : objects.length === 0 ? (
                  <div className="flex-1 flex items-center justify-center py-12">
                    <div className="text-center">
                      <Upload className="w-12 h-12 text-gray-300 mx-auto mb-4" />
                      <h3 className="text-lg font-medium text-gray-900 mb-2">No files yet</h3>
                      <p className="text-gray-500 mb-4">Drag and drop files here or click Upload</p>
                    </div>
                  </div>
                ) : (
                  <table className="w-full">
                    <thead className="bg-gray-50 sticky top-0">
                      <tr>
                        <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">Name</th>
                        <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">Size</th>
                        <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">Modified</th>
                        <th className="px-4 py-3 text-right text-xs font-medium text-gray-500 uppercase tracking-wider">Actions</th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-gray-200">
                      {objects.map((obj: ObjectInfo) => {
                        const FileIcon = getFileIcon(obj.content_type, obj.is_dir)
                        const fileName = obj.key.split('/').filter(Boolean).pop() || obj.key
                        const isImage = isImageFile(obj.content_type)

                        return (
                          <tr
                            key={obj.key}
                            className="hover:bg-gray-50 cursor-pointer"
                            onClick={() => handleObjectClick(obj)}
                          >
                            <td className="px-4 py-3">
                              <div className="flex items-center gap-3">
                                {isImage && !obj.is_dir ? (
                                  <div className="w-10 h-10 rounded bg-gray-100 overflow-hidden flex-shrink-0">
                                    <img
                                      src={`/api/dashboard/storage/buckets/${selectedBucket}/objects/${obj.key}`}
                                      alt={fileName}
                                      className="w-full h-full object-cover"
                                      onError={(e) => {
                                        (e.target as HTMLImageElement).style.display = 'none'
                                      }}
                                    />
                                  </div>
                                ) : (
                                  <FileIcon className={`w-5 h-5 flex-shrink-0 ${obj.is_dir ? 'text-yellow-500' : 'text-gray-400'}`} />
                                )}
                                <span className="text-sm font-medium text-gray-900 truncate">{fileName}</span>
                              </div>
                            </td>
                            <td className="px-4 py-3 text-sm text-gray-500">
                              {obj.is_dir ? '-' : formatBytes(obj.size)}
                            </td>
                            <td className="px-4 py-3 text-sm text-gray-500">
                              {obj.last_modified ? formatDate(obj.last_modified) : '-'}
                            </td>
                            <td className="px-4 py-3 text-right">
                              <div className="flex items-center justify-end gap-1" onClick={(e) => e.stopPropagation()}>
                                {isImage && !obj.is_dir && (
                                  <button
                                    onClick={() => {
                                      const url = `/api/dashboard/storage/buckets/${selectedBucket}/objects/${obj.key}`
                                      setPreviewImage({ url, name: fileName })
                                    }}
                                    className="p-1.5 text-gray-400 hover:text-blue-600 transition-colors"
                                    title="Preview"
                                  >
                                    <Eye className="w-4 h-4" />
                                  </button>
                                )}
                                {!obj.is_dir && (
                                  <>
                                    <button
                                      onClick={() => handleDownload(obj)}
                                      className="p-1.5 text-gray-400 hover:text-blue-600 transition-colors"
                                      title="Download"
                                    >
                                      <Download className="w-4 h-4" />
                                    </button>
                                    <button
                                      onClick={() => handleCopyUrl(obj)}
                                      className="p-1.5 text-gray-400 hover:text-blue-600 transition-colors"
                                      title="Copy URL"
                                    >
                                      <Link className="w-4 h-4" />
                                    </button>
                                  </>
                                )}
                                <button
                                  onClick={() => {
                                    if (confirm(`Delete "${fileName}"?`)) {
                                      deleteObjectMutation.mutate(obj.key)
                                    }
                                  }}
                                  className="p-1.5 text-gray-400 hover:text-red-600 transition-colors"
                                  title="Delete"
                                >
                                  <Trash2 className="w-4 h-4" />
                                </button>
                              </div>
                            </td>
                          </tr>
                        )
                      })}
                    </tbody>
                  </table>
                )}
              </div>
            </>
          )}
        </div>
      </div>

      {/* Create Bucket Modal */}
      {showCreateBucket && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4">
          <div className="bg-white rounded-xl shadow-xl w-full max-w-md">
            <div className="flex items-center justify-between px-6 py-4 border-b border-gray-200">
              <h2 className="text-lg font-semibold">Create New Bucket</h2>
              <button onClick={() => setShowCreateBucket(false)} className="text-gray-500 hover:text-gray-700">
                <X className="w-5 h-5" />
              </button>
            </div>
            <div className="p-6">
              <div className="mb-4">
                <label className="block text-sm font-medium text-gray-700 mb-1">Bucket Name</label>
                <input
                  type="text"
                  value={newBucketName}
                  onChange={(e) => setNewBucketName(e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, ''))}
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-blue-500 outline-none"
                  placeholder="my-bucket"
                />
                <p className="mt-1 text-xs text-gray-500">Lowercase letters, numbers, and hyphens only (3-63 chars)</p>
              </div>
              <div className="mb-4">
                <label className="flex items-center gap-2">
                  <input
                    type="checkbox"
                    checked={newBucketPublic}
                    onChange={(e) => setNewBucketPublic(e.target.checked)}
                    className="rounded"
                  />
                  <span className="text-sm text-gray-700">Make bucket public</span>
                </label>
                <p className="mt-1 text-xs text-gray-500 ml-6">Public buckets allow anyone to read files</p>
              </div>
            </div>
            <div className="flex justify-end gap-3 px-6 py-4 border-t border-gray-200">
              <button
                onClick={() => setShowCreateBucket(false)}
                className="px-4 py-2 text-gray-700 hover:bg-gray-100 rounded-lg transition-colors"
              >
                Cancel
              </button>
              <button
                onClick={() => createBucketMutation.mutate()}
                disabled={!newBucketName || newBucketName.length < 3 || createBucketMutation.isPending}
                className="flex items-center gap-2 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
              >
                {createBucketMutation.isPending && <Loader2 className="w-4 h-4 animate-spin" />}
                Create Bucket
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Create Folder Modal */}
      {showCreateFolder && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4">
          <div className="bg-white rounded-xl shadow-xl w-full max-w-md">
            <div className="flex items-center justify-between px-6 py-4 border-b border-gray-200">
              <h2 className="text-lg font-semibold">Create New Folder</h2>
              <button onClick={() => setShowCreateFolder(false)} className="text-gray-500 hover:text-gray-700">
                <X className="w-5 h-5" />
              </button>
            </div>
            <div className="p-6">
              <div className="mb-4">
                <label className="block text-sm font-medium text-gray-700 mb-1">Folder Name</label>
                <input
                  type="text"
                  value={newFolderName}
                  onChange={(e) => setNewFolderName(e.target.value)}
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg focus:ring-2 focus:ring-blue-500 focus:border-blue-500 outline-none"
                  placeholder="my-folder"
                />
              </div>
            </div>
            <div className="flex justify-end gap-3 px-6 py-4 border-t border-gray-200">
              <button
                onClick={() => setShowCreateFolder(false)}
                className="px-4 py-2 text-gray-700 hover:bg-gray-100 rounded-lg transition-colors"
              >
                Cancel
              </button>
              <button
                onClick={() => createFolderMutation.mutate()}
                disabled={!newFolderName || createFolderMutation.isPending}
                className="flex items-center gap-2 px-4 py-2 bg-blue-600 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
              >
                {createFolderMutation.isPending && <Loader2 className="w-4 h-4 animate-spin" />}
                Create Folder
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Image Preview Modal */}
      {previewImage && (
        <div
          className="fixed inset-0 bg-black/80 flex items-center justify-center z-50 p-4"
          onClick={() => setPreviewImage(null)}
        >
          <div className="relative max-w-4xl max-h-[90vh]" onClick={(e) => e.stopPropagation()}>
            <button
              onClick={() => setPreviewImage(null)}
              className="absolute -top-10 right-0 text-white hover:text-gray-300 transition-colors"
            >
              <X className="w-6 h-6" />
            </button>
            <img
              src={previewImage.url}
              alt={previewImage.name}
              className="max-w-full max-h-[85vh] object-contain rounded-lg"
            />
            <p className="text-center text-white mt-2 text-sm">{previewImage.name}</p>
          </div>
        </div>
      )}
    </div>
  )
}
