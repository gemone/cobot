package memory

// Static SQL queries used across the memory package.
// Extracted from inline strings for maintainability.

// --- Wings ---

const sqlInsertWing = `INSERT INTO wings (id, name, type, keywords_json) VALUES (?, ?, ?, ?)`
const sqlSelectWing = `SELECT id, name, type, keywords_json FROM wings WHERE id = ?`
const sqlSelectWingByName = `SELECT id, name, type, keywords_json FROM wings WHERE name = ?`
const sqlSelectWings = `SELECT id, name, type, keywords_json FROM wings ORDER BY name`

// --- Rooms ---

const sqlInsertRoom = `INSERT INTO rooms (id, wing_id, name, hall_type) VALUES (?, ?, ?, ?)`
const sqlSelectRooms = `SELECT id, wing_id, name, hall_type FROM rooms WHERE wing_id = ? ORDER BY name`
const sqlSelectRoom = `SELECT id, wing_id, name, hall_type FROM rooms WHERE id = ? AND wing_id = ?`
const sqlSelectRoomByName = `SELECT id, wing_id, name, hall_type FROM rooms WHERE wing_id = ? AND name = ?`
const sqlDeleteRoomByName = `DELETE FROM rooms WHERE wing_id = ? AND name = ?`

// --- Drawers ---

const sqlInsertDrawer = `INSERT INTO drawers (id, room_id, content, tag, created_at) VALUES (?, ?, ?, ?, ?)`
const sqlDeleteDrawer = `DELETE FROM drawers WHERE id = ?`
const sqlSelectDrawersByRoomOrdered = `SELECT d.id, d.room_id, d.content, d.created_at FROM drawers d WHERE d.room_id = ? ORDER BY d.created_at ASC`

// --- Closets ---

const sqlInsertCloset = `INSERT INTO closets (id, room_id, summary) VALUES (?, ?, ?)`
const sqlInsertClosetDrawer = `INSERT INTO closet_drawers (closet_id, drawer_id, position) VALUES (?, ?, ?)`
const sqlSelectClosets = `SELECT id, room_id, summary FROM closets WHERE room_id = ?`
const sqlSelectClosetDrawers = `SELECT drawer_id FROM closet_drawers WHERE closet_id = ? ORDER BY position`
